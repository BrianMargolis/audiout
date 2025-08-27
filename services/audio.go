package services

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"go.uber.org/zap"
)

// AudioDevice handles all audio device operations
type AudioDevice interface {
	Get(ctx context.Context) (string, error)
	List(ctx context.Context) ([]string, error)
	Set(ctx context.Context, deviceName string) error
}

type audioDevice struct {
	log *zap.SugaredLogger
}

func NewAudioDevice(log *zap.SugaredLogger) AudioDevice {
	return &audioDevice{log: log}
}

func (s *audioDevice) Get(ctx context.Context) (string, error) {
	out, err := runCmd(ctx, "SwitchAudioSource", "-c", "-t", "output")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (s *audioDevice) List(ctx context.Context) ([]string, error) {
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

func (s *audioDevice) Set(ctx context.Context, deviceName string) error {
	_, err := runCmd(ctx, "SwitchAudioSource", "-s", deviceName, "-t", "output")
	return err
}

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
