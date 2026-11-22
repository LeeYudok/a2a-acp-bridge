package board

import (
	"errors"
	"fmt"
	"time"
)

var ErrPoolFull = errors.New("위임 풀 상한 도달")
var ErrPoolSetForbidden = errors.New("pool set 에는 user 또는 활성 지휘자 권한이 필요합니다")

type PoolFamilyStatus struct {
	Min       int  `json:"min"`
	Max       *int `json:"max"`
	Running   int  `json:"running"`
	FreeSlots *int `json:"free_slots"`
	BelowMin  bool `json:"below_min"`
}

type PoolStatus struct {
	MaxTotal  *int                        `json:"max_total"`
	Running   int                         `json:"running"`
	FreeSlots *int                        `json:"free_slots"`
	Families  map[string]PoolFamilyStatus `json:"families"`
}

// familyFor resolves the current family of an assignee, including legacy tasks.
func familyFor(families map[string]string, assignee string) string {
	if family := families[assignee]; family != "" {
		return family
	}
	return assignee
}

func runningTasks(tasks []Task, families map[string]string) (map[string]int, int) {
	counts := make(map[string]int)
	total := 0
	for _, task := range tasks {
		if task.State == "working" {
			counts[familyFor(families, task.Assignee)]++
			total++
		}
	}
	return counts, total
}

func slots(limit *int, running int) *int {
	if limit == nil {
		return nil
	}
	free := max(0, *limit-running)
	return &free
}

func (cfg poolConfig) status(tasks []Task, families map[string]string) PoolStatus {
	counts, total := runningTasks(tasks, families)
	status := PoolStatus{
		MaxTotal: cfg.MaxTotal, Running: total, FreeSlots: slots(cfg.MaxTotal, total),
		Families: make(map[string]PoolFamilyStatus),
	}
	for _, family := range families {
		counts[family] += 0
	}
	for family := range cfg.Families {
		counts[family] += 0
	}
	for family, running := range counts {
		entry := PoolFamilyStatus{Running: running, FreeSlots: status.FreeSlots}
		if bounds := cfg.bounds(family); bounds != nil {
			entry.Min, entry.Max = bounds.Min, &bounds.Max
			entry.FreeSlots = slots(entry.Max, running)
			if status.FreeSlots != nil && *status.FreeSlots < *entry.FreeSlots {
				entry.FreeSlots = status.FreeSlots
			}
			entry.BelowMin = running < bounds.Min
		}
		status.Families[family] = entry
	}
	return status
}

// PoolStatus reads one consistent view of settings and working tasks.
func (b *Board) PoolStatus(families map[string]string) (PoolStatus, error) {
	var out PoolStatus
	err := b.withPoolState(func(_ string, cfg poolConfig, st *boardState) error {
		out = cfg.status(st.Tasks, families)
		return nil
	})
	return out, err
}

// StartDelegation checks capacity and records the working task and message in one board transaction.
func (b *Board) StartDelegation(from, to, prompt, session string, families map[string]string) (Message, error) {
	var msg Message
	err := b.withPoolState(func(_ string, cfg poolConfig, st *boardState) error {
		family := familyFor(families, to)
		counts, total := runningTasks(st.Tasks, families)
		if bounds := cfg.bounds(family); bounds != nil && counts[family] >= bounds.Max {
			return fmt.Errorf("%w: family=%s running=%d max=%d", ErrPoolFull, family, counts[family], bounds.Max)
		}
		if cfg.MaxTotal != nil && total >= *cfg.MaxTotal {
			return fmt.Errorf("%w: family=%s total_running=%d max_total=%d", ErrPoolFull, family, total, *cfg.MaxTotal)
		}
		var err error
		msg, err = addMessage(st, from, to, prompt, "delegate", "", "working", session)
		if err != nil {
			return err
		}
		return b.save(*st)
	})
	return msg, err
}

// SetPool replaces family bounds or the global limit and returns the resulting status.
func (b *Board) SetPool(actor, family string, bounds *PoolBounds, maxTotal *int, families map[string]string) (PoolStatus, error) {
	if (bounds == nil) == (maxTotal == nil) {
		return PoolStatus{}, fmt.Errorf("pool: 계열 또는 전체 상한 중 하나를 지정해야 합니다")
	}
	if actor == "" {
		return PoolStatus{}, ErrPoolSetForbidden
	}
	if actor == "user" {
		return b.setPool(family, bounds, maxTotal, families)
	}

	if err := b.ensureCoordMigrated(); err != nil {
		return PoolStatus{}, err
	}
	var out PoolStatus
	err := b.withCoordLock(func() error {
		lease, _, err := b.loadCoordFile()
		if err != nil {
			return err
		}
		if CoordinatorStatus(lease, time.Now()) != "active" || lease.Leader != actor {
			return ErrPoolSetForbidden
		}
		out, err = b.setPool(family, bounds, maxTotal, families)
		return err
	})
	return out, err
}

func (b *Board) setPool(family string, bounds *PoolBounds, maxTotal *int, families map[string]string) (PoolStatus, error) {
	path, err := b.poolConfigPath()
	if err != nil {
		return PoolStatus{}, err
	}
	unlock, err := lockFile(path)
	if err != nil {
		return PoolStatus{}, err
	}
	defer unlock()

	var out PoolStatus
	err = b.withPoolState(func(path string, cfg poolConfig, st *boardState) error {
		if maxTotal != nil {
			cfg.MaxTotal = maxTotal
		} else {
			if cfg.Families == nil {
				cfg.Families = make(map[string]PoolBounds)
			}
			cfg.Families[family] = *bounds
		}
		if err := cfg.validate(); err != nil {
			return err
		}
		if err := writeAtomicJSON(path, cfg, "pool"); err != nil {
			return err
		}
		out = cfg.status(st.Tasks, families)
		return nil
	})
	return out, err
}

func (b *Board) withPoolState(fn func(string, poolConfig, *boardState) error) error {
	path, err := b.poolConfigPath()
	if err != nil {
		return err
	}
	return b.withLock(func() error {
		cfg, err := loadPool(path)
		if err != nil {
			return err
		}
		st, err := b.load()
		if err != nil {
			return err
		}
		return fn(path, cfg, &st)
	})
}
