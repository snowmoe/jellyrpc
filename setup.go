package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
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

func runSetup() error {
	uid := os.Geteuid()
	if uid == 0 {
		return errors.New("running as root, try again as a user")
	}

	cfgPath, err := GetConfigPath()
	if err != nil {
		return err
	}

	jellyfinURL := ""
	cfg, _, err := loadConfig(cfgPath)
	if err == nil {
		jellyfinURL = cfg.JellyfinURL
	}

	// inputs
	p := NewPrompt(os.Stdin, os.Stdout)

	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	c, err := askServer(ctx, p, jellyfinURL)
	if err != nil {
		return err
	}

	hasQC, err := c.QuickConnectEnabled(ctx)
	if err != nil {
		return err
	}

	var useQC bool
	if hasQC {
		useQC, err = p.BoolWithChars("use (Q)uick Connect or paste a (k)ey?", true, "q", "k")
		if err != nil {
			return err
		}
	}

	if useQC {
		auth, err := askQuickConnect(ctx, p, c)
		if err != nil {
			return err
		}

		c.APIKey = auth.Token
		c.UserName = auth.User.Name

		user, err := c.CurrentUser(ctx)
		if err != nil {
			return err
		}

		fmt.Fprintf(p.out, "authentication successful for user: %s\n", user.Name)
	} else {
		return errors.New("key setup not implemented yet")
	}

	return nil
}

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

func askServer(ctx context.Context, p *Prompt, url string) (*jellyfin.Client, error) {
	def := url
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
