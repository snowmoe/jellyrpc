package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/snowmoe/jellyrpc/internal/discord"
	"github.com/snowmoe/jellyrpc/internal/jellyfin"
)

type status int

const (
	statusOK status = iota + 1
	statusWarn
	statusFail
	statusSkip
)

func (s status) String() string {
	switch s {
	case statusOK:
		return "  OK"
	case statusWarn:
		return "WARN"
	case statusFail:
		return "FAIL"
	case statusSkip:
		return "SKIP"
	default:
		return "UNKN"
	}
}

type result struct {
	name   string
	status status
	msg    string
}

func printResult(r result) {
	fmt.Printf("%-24s %s\n",
		fmt.Sprintf("%s: %s", r.status, r.name),
		r.msg,
	)
}

var errCheckFailed = errors.New("check failed")

// result helpers

func fail(msg string) result {
	return result{
		status: statusFail,
		msg:    msg,
	}
}

func warn(msg string) result {
	return result{
		status: statusWarn,
		msg:    msg,
	}
}

func ok(msg string) result {
	return result{
		status: statusOK,
		msg:    msg,
	}
}

func skip(msg string) result {
	return result{
		status: statusSkip,
		msg:    msg,
	}
}

// check runner

func runCheck() error {
	var (
		// if ANY check has failed
		failed = false

		// if the main daemon chain is broken if a
		// dependant check fails we can't check
		// anything past that, so we can skip based on this
		chain = true

		cfg    *Config
		client *jellyfin.Client
		user   *jellyfin.User

		ctx  context.Context
		stop context.CancelFunc

		appID string
	)

	// takes a result, prints it, sets failed
	// to true if the result status was a fail
	// returns true/false depending on fail state
	report := func(r result) bool {
		printResult(r)

		if r.status == statusFail {
			failed = true
			return false
		}

		return true
	}

	// skips a check if the chain is broken
	// otherwise runs a check and reports it,
	// setting chain to false if that failed
	step := func(name string, fn func() result) {
		var r result
		// if chain == false, we skip
		if !chain {
			r = skip("earlier check failed")
		} else {
			// run the test func, if report returned false then
			// the check failed and we set chain to false to skip
			// future checks that step
			r = fn()
		}

		r.name = name
		if !report(r) {
			chain = false
		}
	}

	// config checks

	cfgPath, err := GetConfigPath()
	if err != nil {
		// return this directly because somethings seriously fucked
		// if we can't even build the config path
		return err
	}

	step("config file", func() result {
		r, c := checkConfigFile(cfgPath)
		cfg = c
		return r
	})

	step("config values", func() result { return checkConfigValues(cfg) })

	// jf client checks

	if chain {
		client = jellyfin.NewClient(cfg.JellyfinURL, cfg.JellyfinKey, gitVersion)
		client.UserName = cfg.JellyfinUser

		ctx, stop = signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
	}

	step("jellyfin server", func() result { return checkServer(ctx, client) })

	step("user token", func() result {
		r, u := checkToken(ctx, client)
		user = u
		return r
	})

	step("jellyfin user", func() result { return checkUser(ctx, client, user) })

	step("active playing", func() result { return checkPlaying(ctx, client) })

	// discord checks

	// we set chain back to true since discord checks aren't dependant on the
	// jellyfin chain, but they do have their own chain (handshake can't run if socket)
	// discovery failed
	chain = true

	step("socket", func() result { return checkSocket() })

	if cfg == nil {
		appID = defaultAppID
	} else {
		appID = cfg.AppID
	}

	step("rpc handshake", func() result { return checkHandshake(appID) })

	// if any failed then return an error so we can exit 1 in main
	if failed {
		return errCheckFailed
	}

	return nil
}

// check functions

// client

// gets the public server info and reports jf server name and version
// if this fails then in most cases there is no reachable jellyfin
// server, unless there's reverse proxy rules or similar
func checkServer(ctx context.Context, c *jellyfin.Client) result {
	info, err := c.PublicSystemInfo(ctx)
	if err != nil {
		return fail(err.Error())
	}

	msg := fmt.Sprintf("%s (v%s)", info.ServerName, info.Version)
	return ok(msg)
}

// attempts /Users/Me with the key, returning a pointer to user if it was a
// user token otherwise skips if we get 400 from jellyfin (using an api key)
func checkToken(ctx context.Context, c *jellyfin.Client) (result, *jellyfin.User) {
	user, err := c.CurrentUser(ctx)
	if statErr, ok := errors.AsType[*jellyfin.StatusError](err); ok {
		switch statErr.Code {
		case 400:
			return skip("using api key"), nil
		case 401:
			return fail("token rejected"), nil
		default:
			return fail(err.Error()), nil
		}
	} else if err != nil {
		return fail(err.Error()), nil
	}

	return ok("user: " + user.Name), &user
}

// attempts /Users with the key and verifies if the username in config
// exists as a jellyfin user, if we're using a user token then we warn
// on config name mismatch, otherwise skipping the check as we already know
// the username from /Users/Me
func checkUser(ctx context.Context, c *jellyfin.Client, user *jellyfin.User) result {
	// nil user would mean we're using an api key
	if user != nil {
		if !strings.EqualFold(c.UserName, user.Name) {
			msg := fmt.Sprintf("mismatched usernames (token: %s, config: %s)", user.Name, c.UserName)
			return warn(msg)
		}

		return skip("using token")
	}

	users, err := c.Users(ctx)
	// no need to check status, api keys are always privileged (afaik)
	// so if this fails the key won't work full stop
	if err != nil {
		return fail(err.Error())
	}

	for _, u := range users {
		if strings.EqualFold(u.Name, c.UserName) {
			return ok(fmt.Sprintf("user '%s' exists", u.Name))
		}
	}

	return fail(fmt.Sprintf("user '%s' doesn't exist", c.UserName))
}

func checkPlaying(ctx context.Context, c *jellyfin.Client) result {
	var msg string

	s, err := c.ActiveSession(ctx)
	if err != nil {
		return fail(err.Error())
	}

	if s.NowPlayingItem.Name == "" {
		return ok("nothing playing")
	}

	if s.NowPlayingItem.SeriesName != "" {
		msg = fmt.Sprintf("currently playing: %s", s.NowPlayingItem.SeriesName)
	} else {
		msg = fmt.Sprintf("currently playing: %s", s.NowPlayingItem.Name)
	}

	return ok(msg)
}

// discord

func checkSocket() result {
	conn, err := discord.DiscoverIPCSocket()
	if err != nil {
		return fail("discord not running?")
	}

	conn.Close()
	return ok("found discord socket")
}

func checkHandshake(appID string) result {
	var msg string

	dc, err := discord.NewConn(appID, gitVersion)
	if err != nil {
		return fail(err.Error())
	}

	dc.Close()

	if appID == defaultAppID {
		msg = "handshake successful (default app id)"
	} else {
		msg = "handshake successful"
	}
	return ok(msg)
}

// config

func checkConfigFile(path string) (result, *Config) {
	cfg, unknown, err := loadConfig(path)
	if errors.Is(err, os.ErrNotExist) {
		return fail("config file doesn't exist, try 'jellyrpc setup'"), nil
	} else if err != nil {
		return fail(err.Error()), nil
	}

	if len(unknown) > 0 {
		msg := fmt.Sprintf("unknown config key(s): %s", strings.Join(unknown, ", "))
		return warn(msg), cfg
	}

	return ok(""), cfg
}

func checkConfigValues(cfg *Config) result {
	cfg.ApplyDefaults(defaultAppID)

	missing, err := cfg.Validate()
	if err != nil {
		msg := fmt.Sprintf("%s: %s", err, strings.Join(missing, ", "))
		return fail(msg)
	}

	return ok("")
}
