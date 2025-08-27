package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	"brianmargolis.com/audiout/services"
	"brianmargolis.com/audiout/utils"
)

const DEFAULT_CONFIG_PATH = "~/.config/.audiout.yaml"
const CONFIG_PATH_ENV = "AUDIOUT_CONFIG"

func main() {
	verbose, toggle, err := parseArgs()
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

	configService := services.NewConfigService(log)
	config, err := configService.Load(cfgPath)
	if err != nil {
		log.Errorw("config load failed (continuing with defaults)", "path", cfgPath, "err", err)
	}
	log.Infow("config loaded", "config", config, "toggle", toggle)

	audioDeviceService := services.NewAudioDevice(log)
	pickerService := services.NewPicker(configService, log)

	currentDevice, err := audioDeviceService.Get(ctx)
	if err != nil {
		log.Errorw("failed to query current output device", "err", err)
		os.Exit(1)
	}
	log.Infow("current device", "currentDevice", currentDevice)

	devices, err := audioDeviceService.List(ctx)
	if err != nil {
		log.Errorw("failed to list output devices", "err", err)
		os.Exit(1)
	}
	log.Infow("devices found (pre-filter)", "count", len(devices))

	// ----- build choices -----
	choices := configService.BuildChoices(devices)
	if len(choices) == 0 {
		log.Error("no selectable output devices after filtering")
		os.Exit(1)
	}
	log.Infow("choices (post-filter)", "count", len(choices))

	// ----- pick device -----
	choice, ok, err := pickerService.PickDevice(ctx, choices, currentDevice, toggle)
	if err != nil {
		log.Errorw("device selection failed", "err", err)
		os.Exit(1)
	}
	if !ok {
		log.Infow("no selection; exiting")
		return
	}
	log.Infow("selected", "friendly", choice.FriendlyName, "real", choice.RealName)

	// ----- switch -----
	if err := audioDeviceService.Set(ctx, choice.RealName); err != nil {
		log.Errorw("failed to switch output device", "target", choice.RealName, "err", err)
		os.Exit(1)
	}
	log.Infow("switched", "to", choice.FriendlyName)
	fmt.Printf("Output -> %s\n", choice.FriendlyName)
}

// -------- arg parsing and logging --------
func parseArgs() (bool, bool, error) {
	var verbose, toggle bool
	flag.BoolVar(&verbose, "v", false, "verbose logging")
	flag.BoolVar(&toggle, "t", false, "toggle mode: switch to next audio device alphabetically")
	flag.Parse()
	return verbose, toggle, nil
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

func checkDependencies(log *zap.SugaredLogger) error {
	if err := utils.RequireBinary("SwitchAudioSource"); err != nil {
		return fmt.Errorf("missing dependency: SwitchAudioSource (hint: brew install switchaudio-osx): %w", err)
	}
	log.Debug("ok: SwitchAudioSource present")
	if err := utils.RequireBinary("fzf"); err != nil {
		return fmt.Errorf("missing dependency: fzf (hint: brew install fzf): %w", err)
	}
	log.Debug("ok: fzf present")
	return nil
}
