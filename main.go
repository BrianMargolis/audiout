package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

type Config struct {
	FriendlyNames map[string]string `yaml:"friendly"`
	Ignored       []string          `yaml:"ignored"`
}

type Choice struct {
	// RealName is the actual system name, used for switching.
	RealName string
	// FriendlyName is what the user sees in the UI, overwritten by the config.
	FriendlyName string
}

const DEFAULT_CONFIG_PATH = "~/.config/.audiout.yaml"
const CONFIG_PATH_ENV = "AUDIOUT_CONFIG"

func main() {
	verbose, err := parseArgs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "arg parse failed: %v\n", err)
		os.Exit(1)
	}

	log, logCloser, err := constructLogger(verbose)
	if err != nil {
		fmt.Fprintf(os.Stderr, "logger init failed: %v\n", err)
		os.Exit(1)
	}
	defer logCloser()

	// config path
	cfgPath := os.Getenv(CONFIG_PATH_ENV)
	if cfgPath == "" {
		cfgPath = DEFAULT_CONFIG_PATH
	}

	log.Infow("start", "verbose", verbose, "config", cfgPath)

	// listen for interrupts
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		log.Infow("signal received, cancelling")
		cancel()
	}()

	if err := checkDependencies(log); err != nil {
		log.Errorw("dependency check failed", "err", err)
		os.Exit(1)
	}
	log.Debug("all dependencies present")

	cfg, err := loadConfig(cfgPath, log)
	if err != nil {
		log.Errorw("config load failed (continuing with defaults)", "path", cfgPath, "err", err)
		cfg = &Config{}
	}
	log.Infow("config loaded", "config", cfg)

	// ----- get the current device -----
	cur, err := currentOutput(ctx, log)
	if err != nil {
		log.Errorw("failed to query current output device", "err", err)
		os.Exit(1)
	}
	curDisp := friendlyOf(cur, cfg)
	log.Infow("current device", "real", cur, "friendly", curDisp)

	// ----- get all devices -----
	devs, err := listOutputs(ctx, log)
	if err != nil {
		log.Errorw("failed to list output devices", "err", err)
		os.Exit(1)
	}
	log.Infow("devices found (pre-filter)", "count", len(devs))

	// ----- build choices -----
	choices := buildChoices(devs, cfg, log)
	if len(choices) == 0 {
		log.Error("no selectable output devices after filtering")
		os.Exit(1)
	}
	log.Infow("choices (post-filter)", "count", len(choices))

	// ----- fzf -----
	choice, ok, err := pick(ctx, choices, curDisp, log)
	if err != nil {
		log.Errorw("fzf selection failed", "err", err)
		os.Exit(1)
	}
	if !ok {
		log.Infow("no selection; exiting")
		return
	}
	log.Infow("selected", "friendly", choice.FriendlyName, "real", choice.RealName)

	// ----- switch -----
	if err := switchOutput(ctx, choice.RealName, log); err != nil {
		log.Errorw("failed to switch output device", "target", choice.RealName, "err", err)
		os.Exit(1)
	}
	log.Infow("switched", "to", choice.FriendlyName)
	fmt.Printf("Output -> %s\n", choice.FriendlyName)
}

// -------- arg parsing and logging --------
func parseArgs() (bool, error) {
	var verbose bool
	flag.BoolVar(&verbose, "v", false, "verbose logging")
	flag.Parse()
	return verbose, nil
}

func constructLogger(verbose bool) (
	*zap.SugaredLogger,
	func() error,
	error,
) {
	var lg *zap.Logger
	var err error
	if verbose {
		lg, err = zap.NewDevelopment()
	} else {
		cfg := zap.NewProductionConfig()
		cfg.Level = zap.NewAtomicLevelAt(zap.WarnLevel)
		// human-readable console output
		cfg.Encoding = "console"
		lg, err = cfg.Build()
	}
	if err != nil {
		return nil, func() error { return nil }, err
	}
	return lg.Sugar(), lg.Sync, nil
}

// -------- dependencies --------
func checkDependencies(log *zap.SugaredLogger) error {
	if err := requireBinary("SwitchAudioSource"); err != nil {
		return fmt.Errorf("missing dependency: SwitchAudioSource (hint: brew install switchaudio-osx): %w", err)
	}
	log.Debug("ok: SwitchAudioSource present")
	if err := requireBinary("fzf"); err != nil {
		return fmt.Errorf("missing dependency: fzf (hint: brew install fzf): %w", err)
	}
	log.Debug("ok: fzf present")
	return nil
}

func requireBinary(name string) error {
	_, err := exec.LookPath(name)
	return err
}

// -------- config --------
func loadConfig(path string, log *zap.SugaredLogger) (*Config, error) {
	path = expandPath(path)
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Infow("config not found; using defaults", "path", path)
			return &Config{}, nil
		}
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return &Config{
			FriendlyNames: map[string]string{},
		}, err

	}
	if c.FriendlyNames == nil {
		c.FriendlyNames = map[string]string{}
	}
	return &c, nil
}

func expandPath(p string) string {
	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// -------- friendly/ignored helpers --------

func isIgnored(name string, cfg *Config) bool {
	for _, n := range cfg.Ignored {
		if name == n {
			return true
		}
	}
	return false
}

func friendlyOf(real string, cfg *Config) string {
	if f, ok := cfg.FriendlyNames[real]; ok && f != "" {
		return f
	}
	return real
}

func buildChoices(devs []string, cfg *Config, log *zap.SugaredLogger) []Choice {
	var out []Choice
	for _, d := range devs {
		if isIgnored(d, cfg) {
			log.Debugw("ignored device", "name", d)
			continue
		}
		out = append(out, Choice{
			FriendlyName: friendlyOf(d, cfg),
			RealName:     d,
		})
	}
	return out
}

// -------- SwitchAudioSource wrappers --------

func runCmd(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("%s timed out", name)
	}
	if err != nil {
		if errBuf.Len() > 0 {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(errBuf.String()))
		}
		return "", err
	}
	return outBuf.String(), nil
}

func currentOutput(ctx context.Context, log *zap.SugaredLogger) (string, error) {
	out, err := runCmd(ctx, "SwitchAudioSource", "-c", "-t", "output")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func listOutputs(ctx context.Context, log *zap.SugaredLogger) ([]string, error) {
	out, err := runCmd(ctx, "SwitchAudioSource", "-a", "-t", "output")
	if err != nil {
		return nil, err
	}
	var devs []string
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		name := strings.TrimSpace(sc.Text())
		if name != "" {
			devs = append(devs, name)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return devs, nil
}

func switchOutput(ctx context.Context, real string, log *zap.SugaredLogger) error {
	_, err := runCmd(ctx, "SwitchAudioSource", "-s", real, "-t", "output")
	return err
}

// -------- fzf --------

func pick(ctx context.Context, choices []Choice, currentFriendly string, log *zap.SugaredLogger) (Choice, bool, error) {
	var b strings.Builder
	for _, c := range choices {
		// FRIENDLY \t REAL
		fmt.Fprintf(&b, "%s\t%s\n", c.FriendlyName, c.RealName)
	}
	input := b.String()

	args := []string{
		"--prompt", "🎧 Output: ",
		"--header", "Current: " + currentFriendly,
		"--height", "40%",
		"--reverse",
		"--delimiter", "\t",
		"--with-nth", "1",
		"--bind", "enter:accept",
		"--exact",
	}
	cmd := exec.CommandContext(ctx, "fzf", args...)
	cmd.Stdin = strings.NewReader(input)

	var out bytes.Buffer
	cmd.Stdout = &out
	var errb bytes.Buffer
	cmd.Stderr = &errb

	if err := cmd.Run(); err != nil {
		// Exit status 130/1 when ESC/ctrl-c/no select is normal; detect no output
		if out.Len() == 0 && (ctx.Err() == nil) {
			return Choice{}, false, nil
		}
		if ctx.Err() == context.DeadlineExceeded {
			return Choice{}, false, fmt.Errorf("fzf timed out")
		}
		return Choice{}, false, fmt.Errorf("fzf error: %v: %s", err, strings.TrimSpace(errb.String()))
	}

	line := strings.TrimSpace(out.String())
	if line == "" {
		return Choice{}, false, nil
	}
	parts := strings.SplitN(line, "\t", 2)
	if len(parts) != 2 {
		return Choice{}, false, fmt.Errorf("unexpected fzf line: %q", line)
	}
	return Choice{FriendlyName: parts[0], RealName: parts[1]}, true, nil
}
