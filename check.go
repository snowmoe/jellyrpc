package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
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
	fmt.Printf("%s: %s    %-16s\n",
		r.status,
		r.name,
		r.msg,
	)
}

var errCheckFailed = errors.New("check failed")

// result helpers

func fail(name, msg string) result {
	return result{
		name:   name,
		status: statusFail,
		msg:    msg,
	}
}

func warn(name, msg string) result {
	return result{
		name:   name,
		status: statusWarn,
		msg:    msg,
	}
}

func ok(name string) result {
	return result{
		name:   name,
		status: statusOK,
	}
}

func skip(name string) result {
	return result{
		name:   name,
		status: statusSkip,
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

		cfg *Config
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
		// if chain == false, we skip
		if !chain {
			report(skip(name))
			return
		}

		// run the test func, if report returned false then
		// the check failed and we set chain to false to skip
		// future checks that step
		if !report(fn()) {
			chain = false
		}
	}

	// checks

	cfgPath, err := GetConfigPath()
	if err != nil {
		// return this directly because there's literally no config file
		return err
	}

	// chain checks

	step("config file", func() result {
		r, c := checkConfigFile(cfgPath)
		cfg = c
		return r
	})

	step("config values", func() result { return checkConfigValues(cfg) })

	// if any failed then return an error so we can exit 1 in main
	if failed {
		return errCheckFailed
	}

	return nil
}

// check functions

func checkConfigFile(path string) (result, *Config) {
	name := "config file"

	cfg, unknown, err := loadConfig(path)
	if errors.Is(err, os.ErrNotExist) {
		return fail(name, "config file doesn't exist, try 'jellyrpc setup'"), nil
	} else if err != nil {
		return fail(name, err.Error()), nil
	}

	if len(unknown) > 0 {
		msg := fmt.Sprintf("unknown config key(s): %s", strings.Join(unknown, ", "))
		return warn(name, msg), cfg
	}

	return ok(name), cfg
}

func checkConfigValues(cfg *Config) result {
	name := "config values"

	cfg.ApplyDefaults(defaultAppID)

	missing, err := cfg.Validate()
	if err != nil {
		msg := fmt.Sprintf("%s: %s", err, strings.Join(missing, ", "))
		return fail(name, msg)
	}

	return ok(name)
}
