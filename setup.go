package main

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/snowmoe/jellyrpc/internal/jellyfin"
)

type Prompt struct {
	in  *bufio.Reader
	out io.Writer
}

func NewPrompt(in io.Reader, out io.Writer) *Prompt {
	return &Prompt{
		in:  bufio.NewReader(in),
		out: out,
	}
}

//go:embed config.example
var exampleCfg string

func runSetup() error {
	uid := os.Geteuid()
	if uid == 0 {
		return errors.New("running as root, try again as a user")
	}

	cfgPath, err := ConfigPath()
	if err != nil {
		return err
	}

	baseCfg, jellyfinURL, err := configSource(cfgPath)
	if err != nil {
		return err
	}

	p := NewPrompt(os.Stdin, os.Stdout)
	ctx := context.Background()

	c, err := askServer(ctx, p, jellyfinURL)
	if err != nil {
		return err
	}

	// setup authentication (qc or api key)
	err = authenticate(ctx, p, c)
	if err != nil {
		return err
	}

	// update/create the config with the new auth, and save
	err = saveConfig(cfgPath, baseCfg, c)
	if err != nil {
		return err
	}

	fmt.Fprintf(p.out, "\nwrote new config to %s\n", cfgPath)
	return nil
}

// saveConfig updates/fills JELLYFIN_URL, JELLYFIN_USER, and JELLYFIN_KEY from
// the provided jellyfin.Client into the provided source.
// then atomically writes the new config to the path provided
func saveConfig(path, src string, c *jellyfin.Client) error {
	// if we got this far we can just overwrite the url anyway, and if
	// anything it'll be cleaner, as it's sanitised already
	newValues := map[string]string{
		"JELLYFIN_URL":  c.BaseURL,
		"JELLYFIN_USER": c.UserName,
		"JELLYFIN_KEY":  c.APIKey,
	}

	// src being either the example, or an existing one we loaded
	newCfg := updateConfig(src, newValues)

	cfgDir := filepath.Dir(path)

	err := os.MkdirAll(cfgDir, 0o700)
	if err != nil {
		return err
	}

	return writeFileAtomic(path, []byte(newCfg))
}

// authenticate checks if quick connect is enabled, and prompts to setup
// quick connect or setup with an api key. will set new key + username
// in the jellyfin.Client provided
func authenticate(ctx context.Context, p *Prompt, c *jellyfin.Client) error {
	var useQC bool

	// check if quick connect is enabled
	hasQC, err := c.QuickConnectEnabled(ctx)
	if err != nil {
		return err
	}

	if hasQC {
		useQC, err = p.BoolWithChars("use (Q)uick Connect or paste a (k)ey?", true, "q", "k")
		if err != nil {
			return err
		}
	}

	if !useQC {
		// TODO implement api key setup
		return errors.New("key setup not implemented yet")
	}

	auth, err := askQuickConnect(ctx, p, c)
	if err != nil {
		return err
	}

	// set the client key and name, the url is already set from askServer
	c.APIKey = auth.Token
	c.UserName = auth.User.Name

	user, err := c.CurrentUser(ctx)
	if err != nil {
		return err
	}

	fmt.Fprintf(p.out, "authentication successful for user: %s\n", user.Name)

	return nil
}

// writeFileAtomic atomically writes data into the file path provided,
// creating a temp file beside the file path, before renaming over top.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return err
	}
	defer tmp.Close()

	tmpPath := tmp.Name()
	// doesn't matter if this errors since it's just a best effort
	// of cleaning up if anything goes wrong
	defer os.Remove(tmpPath)

	_, err = tmp.Write(data)
	if err != nil {
		return err
	}

	// sync and close explicity
	err = tmp.Sync()
	if err != nil {
		return err
	}
	err = tmp.Close()
	if err != nil {
		return err
	}

	err = os.Rename(tmpPath, path)
	if err != nil {
		return err
	}

	return nil
}

// updateConfig takes a source config, and a map of new values, updates any
// existing lines with the new values, and appending anything new
func updateConfig(src string, values map[string]string) string {
	var b strings.Builder
	doneKeys := make(map[string]bool)

	// scan through lines, leaving anything we don't need
	// to update written straight back as is,
	// updating any lines that we need to update
	for line := range strings.Lines(src) {
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			b.WriteString(line)
			continue
		}

		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			b.WriteString(line)
			continue
		}

		key := strings.TrimSpace(parts[0])

		val, ok := values[key]
		if ok {
			updatedLine := fmt.Sprintf("%s=%s\n", key, val)
			b.WriteString(updatedLine)

			doneKeys[key] = true
		} else {
			b.WriteString(line)
		}
	}

	// if there's no trailing newline then write it otherwise
	// it fucks the appended writes
	if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
		b.WriteString("\n")
	}

	// append any missing lines
	for _, key := range slices.Sorted(maps.Keys(values)) {
		done := doneKeys[key]
		if done {
			continue
		}

		newLine := fmt.Sprintf("%s=%s\n", key, values[key])
		b.WriteString(newLine)
	}

	return b.String()
}

// configSource takes a config path and returns it's contents (if it exists),
// the url (if parseable), and an error. if no config file exists it returns
// the example config and an empty url
func configSource(path string) (src, url string, err error) {
	// attempt to read an existing config file
	existing, err := os.ReadFile(path)
	switch {
	case err == nil:
		// if it exists we attempt to parse it
		cfg, _, err := parseConfig(bytes.NewReader(existing))
		if err == nil {
			// if we parsed okay then set the url from the config
			url = cfg.JellyfinURL
		}

		src = string(existing)
	case errors.Is(err, fs.ErrNotExist):
		// use the example config as src
		src = exampleCfg
	default:
		return "", "", err
	}

	return src, url, nil
}

// waitForQC polls /QuickConnect/Connect to check if the quick connect code
// has been entered
func waitForQC(ctx context.Context, c *jellyfin.Client, interval time.Duration, secret string) error {
	pollCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	for {
		ok, err := c.ConnectQC(pollCtx, secret)
		if err != nil {
			return err
		}

		if ok {
			return nil
		}

		select {
		case <-pollCtx.Done():
			return pollCtx.Err()
		case <-time.After(interval):
		}
	}
}

// askQuickConnect prompts the user to setup quick connect, inititing the qc setup,
// displaying the code and polling until entered with a 3 min expiry, then
// authenticates with the qc secret and returns the authorization (user + token)
func askQuickConnect(ctx context.Context, p *Prompt, c *jellyfin.Client) (jellyfin.Authorization, error) {
	qc, err := c.InitiateQC(ctx)
	if err != nil {
		return jellyfin.Authorization{}, err
	}

	fmt.Fprintf(p.out, `
enter code in jellyfin under profile > Quick Connect

  Quick Connect code: %s

`, qc.Code)
	fmt.Fprintf(p.out, "waiting (3 mins) for Quick Connect code to be entered... ")

	qcCtx, stop := context.WithTimeout(ctx, 3*time.Minute)
	defer stop()

	err = waitForQC(qcCtx, c, 3*time.Second, qc.Secret)
	// get this out of the way to avoid things writing on the waiting line
	if err != nil {
		fmt.Fprintln(p.out)
	}

	// then actually handle da errors
	if errors.Is(err, context.Canceled) {
		return jellyfin.Authorization{}, errors.New("Quick Connect setup cancelled")
	} else if errors.Is(err, context.DeadlineExceeded) {
		return jellyfin.Authorization{}, errors.New("code expired, run setup again")
	} else if err != nil {
		return jellyfin.Authorization{}, err
	}

	auth, err := c.AuthenticateQC(ctx, qc.Secret)
	if err != nil {
		fmt.Fprintln(p.out)
		return jellyfin.Authorization{}, err
	}

	fmt.Fprintln(p.out, "success!")

	return auth, nil
}

// askServer asks for the jellyfin server url and tests it, prompting until confirmed.
// can take a default url to prompt as the initial default.
func askServer(ctx context.Context, p *Prompt, def string) (*jellyfin.Client, error) {
	for {
		// this will loop itself for required if url is empty
		jellyfinURL, err := p.String("jellyfin server url", def)
		if err != nil {
			return nil, err
		}

		def = jellyfin.SanitiseURL(jellyfinURL)

		c := jellyfin.NewClient(def, "", gitVersion)

		info, err := c.PublicSystemInfo(ctx)
		if err == nil {
			fmt.Fprintf(p.out, "using %s (%s)\n\n", def, info.ServerName)
			return c, nil
		}

		fmt.Fprintf(p.out, "can't reach server: %s\n\n", err.Error())
	}
}

func (p *Prompt) input(prompt string) (string, error) {
	fmt.Fprint(p.out, prompt)

	s, err := p.in.ReadString('\n')
	if err != nil {
		fmt.Fprintln(p.out)
	}

	// if we got an EOF but there was something typed before it
	if err == io.EOF && s != "" {
		return strings.TrimSpace(s), nil
	} else if err != nil {
		return "", fmt.Errorf("input cancelled: %w", err)
	}

	return strings.TrimSpace(s), nil
}

func (p *Prompt) String(prompt, def string) (string, error) {
	defStr := fmt.Sprintf(" [%s]", def)
	// no def = required
	if def == "" {
		defStr = " (required)"
	}

	for {
		s, err := p.input(fmt.Sprintf("%s%s: ", prompt, defStr))
		if err != nil {
			return "", err
		}

		if s != "" {
			return s, nil
		} else if def != "" {
			return def, nil
		}

		fmt.Fprintf(p.out, "%s cannot be empty\n", prompt)
	}
}

func (p *Prompt) Bool(prompt string, def bool) (bool, error) {
	return p.BoolWithChars(prompt, def, "y", "n")
}

// did all this just because I wanted arbitrary y/n chars..
func (p *Prompt) BoolWithChars(prompt string, def bool, trueChar, falseChar string) (bool, error) {
	t := strings.ToLower(trueChar)
	f := strings.ToLower(falseChar)

	defStr := fmt.Sprintf("%s/%s", t, strings.ToUpper(f))
	if def {
		defStr = fmt.Sprintf("%s/%s", strings.ToUpper(t), f)
	}

	for {
		s, err := p.input(fmt.Sprintf("%s [%s] ", prompt, defStr))
		if err != nil {
			return false, err
		}

		switch strings.ToLower(s) {
		case "":
			return def, nil
		case t:
			return true, nil
		case f:
			return false, nil
		default:
			fmt.Fprintf(p.out, "enter %s or %s\n", t, f)
		}
	}
}

// for future
func displayBanner() {
	fmt.Printf(`
       (_)__ / / /_ _________  ____
      / / -_) / / // / __/ _ \/ __/
   __/ /\__/_/_/\_, /_/ / .__/\__/ 
  |___/        /___/   /_/         %s

`, gitVersion)
}
