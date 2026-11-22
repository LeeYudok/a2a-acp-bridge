package board

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const DefaultPoolStaleAfter = 5 * time.Minute
const PoolHeartbeatInterval = time.Minute

type PoolBounds struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

type poolConfig struct {
	StaleAfterSeconds *int                  `json:"stale_after_seconds,omitempty"`
	MaxTotal          *int                  `json:"max_total,omitempty"`
	Default           *PoolBounds           `json:"default,omitempty"`
	Families          map[string]PoolBounds `json:"families,omitempty"`
}

func poolPath() (string, error) {
	if path := os.Getenv("A2A_POOL_FILE"); path != "" {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("pool: 홈 디렉터리 확인 실패: %w", err)
	}
	return filepath.Join(home, ".local", "share", "a2a-acp-bridge", "pool.json"), nil
}

func loadPool(path string) (poolConfig, error) {
	var cfg poolConfig
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("pool 파일 읽기 실패: %w", err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("pool JSON 파싱 실패: %w", err)
	}
	return cfg, cfg.validate()
}

func (cfg poolConfig) validate() error {
	if cfg.StaleAfterSeconds != nil && *cfg.StaleAfterSeconds < int(2*PoolHeartbeatInterval/time.Second) {
		return fmt.Errorf("pool: stale_after_seconds 는 하트비트 주기의 두 배인 120 이상이어야 합니다")
	}
	if cfg.MaxTotal != nil && *cfg.MaxTotal < 1 {
		return fmt.Errorf("pool: max_total 은 1 이상이어야 합니다")
	}
	if cfg.Default != nil {
		if err := validateBounds("default", *cfg.Default); err != nil {
			return err
		}
	}
	for family, bounds := range cfg.Families {
		if !isValidSlug(family) {
			return fmt.Errorf("pool: 잘못된 family %q", family)
		}
		if err := validateBounds(family, bounds); err != nil {
			return err
		}
	}
	return nil
}

func validateBounds(family string, bounds PoolBounds) error {
	if bounds.Min < 0 || bounds.Max < 1 || bounds.Min > bounds.Max {
		return fmt.Errorf("pool: %s 는 0 ≤ min ≤ max 및 max ≥ 1 이어야 합니다", family)
	}
	return nil
}

func (cfg poolConfig) bounds(family string) *PoolBounds {
	if bounds, ok := cfg.Families[family]; ok {
		return &bounds
	}
	return cfg.Default
}

// poolConfigPath keeps settings and board/lease lock files separate.
func (b *Board) poolConfigPath() (string, error) {
	path, err := poolPath()
	if err != nil {
		return "", err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", err
	}
	for _, reserved := range []string{b.path, b.coordPath()} {
		absolute, err := filepath.Abs(reserved)
		if err != nil {
			return "", err
		}
		if path == absolute || path == absolute+".lock" || path+".lock" == absolute {
			return "", fmt.Errorf("pool: 설정 파일 경로가 보드 또는 지휘권 저장소와 겹칩니다")
		}
	}
	return path, nil
}

func (cfg poolConfig) staleCutoff() float64 {
	seconds := DefaultPoolStaleAfter.Seconds()
	if cfg.StaleAfterSeconds != nil {
		seconds = float64(*cfg.StaleAfterSeconds)
	}
	return nowSeconds() - seconds
}
