package services

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"go.uber.org/zap"
)

// Choice represents a device choice with real and friendly names
type Choice struct {
	RealName     string
	FriendlyName string
}

// Config represents the application configuration
type Config struct {
	FriendlyNames map[string]string `yaml:"friendly"`
	Ignored       []string          `yaml:"ignored"`
}

// Picker handles device selection logic
type Picker interface {
	PickDevice(ctx context.Context, choices []Choice, current string, toggleMode bool) (Choice, bool, error)
}

type picker struct {
	config *Config
	log    *zap.SugaredLogger
}

func NewPicker(config *Config, log *zap.SugaredLogger) Picker {
	return &picker{config: config, log: log}
}

func (p *picker) PickDevice(ctx context.Context, choices []Choice, current string, toggleMode bool) (Choice, bool, error) {
	if toggleMode {
		return p.toggleNext(choices, current)
	}
	return p.fzfPick(ctx, choices, current)
}

func (p *picker) toggleNext(choices []Choice, current string) (Choice, bool, error) {
	if len(choices) == 0 {
		return Choice{}, false, fmt.Errorf("no choices available")
	}
	
	// Sort choices alphabetically by RealName
	sortedChoices := make([]Choice, len(choices))
	copy(sortedChoices, choices)
	sort.Slice(sortedChoices, func(i, j int) bool {
		return sortedChoices[i].RealName < sortedChoices[j].RealName
	})
	
	// Find current device index
	currentIdx := -1
	for i, choice := range sortedChoices {
		if choice.RealName == current {
			currentIdx = i
			break
		}
	}
	
	// Select next device (wrap around if at end)
	var nextIdx int
	if currentIdx == -1 {
		// Current device not found in choices, default to first
		nextIdx = 0
		p.log.Debugw("current device not in choices, defaulting to first", "current", current)
	} else {
		nextIdx = (currentIdx + 1) % len(sortedChoices)
		p.log.Debugw("toggling to next device", "currentIdx", currentIdx, "nextIdx", nextIdx)
	}
	
	return sortedChoices[nextIdx], true, nil
}

func (p *picker) fzfPick(ctx context.Context, choices []Choice, current string) (Choice, bool, error) {
	currentFriendly := friendlyOf(current, p.config)
	
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

func friendlyOf(real string, config *Config) string {
	if f, ok := config.FriendlyNames[real]; ok && f != "" {
		return f
	}
	return real
}