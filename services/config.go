package services

import (
	"errors"
	"os"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"

	"brianmargolis.com/audiout/utils"
)

// Config represents the application configuration
type Config struct {
	FriendlyNames map[string]string `yaml:"friendly"`
	Ignored       []string          `yaml:"ignored"`
}

// ConfigService handles configuration loading and device name logic
type ConfigService interface {
	Load(path string) (*Config, error)
	IsIgnored(deviceName string) bool
	FriendlyName(deviceName string) string
	BuildChoices(devices []string) []Choice
}

type configService struct {
	config *Config
	log    *zap.SugaredLogger
}

func NewConfigService(log *zap.SugaredLogger) ConfigService {
	return &configService{
		config: &Config{
			FriendlyNames: map[string]string{},
			Ignored:       []string{},
		},
		log: log,
	}
}

func (s *configService) Load(path string) (*Config, error) {
	path = utils.ExpandPath(path)
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.log.Infow("config not found; using defaults", "path", path)
			s.config = &Config{
				FriendlyNames: map[string]string{},
				Ignored:       []string{},
			}
			return s.config, nil
		}
		return nil, err
	}

	var config Config
	if err := yaml.Unmarshal(b, &config); err != nil {
		s.config = &Config{
			FriendlyNames: map[string]string{},
			Ignored:       []string{},
		}
		return s.config, err
	}

	if config.FriendlyNames == nil {
		config.FriendlyNames = map[string]string{}
	}

	s.config = &config
	return s.config, nil
}

func (s *configService) IsIgnored(deviceName string) bool {
	for _, ignored := range s.config.Ignored {
		if deviceName == ignored {
			return true
		}
	}
	return false
}

func (s *configService) FriendlyName(deviceName string) string {
	if friendlyName, ok := s.config.FriendlyNames[deviceName]; ok && friendlyName != "" {
		return friendlyName
	}
	return deviceName
}

func (s *configService) BuildChoices(devices []string) []Choice {
	var choices []Choice
	for _, device := range devices {
		if s.IsIgnored(device) {
			s.log.Debugw("ignored device", "name", device)
			continue
		}
		choices = append(choices, Choice{
			FriendlyName: s.FriendlyName(device),
			RealName:     device,
		})
	}
	return choices
}

