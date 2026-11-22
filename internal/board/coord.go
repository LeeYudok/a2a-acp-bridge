package board

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// DefaultLeaseTTL 은 오케스트레이터 지휘 리스 기본 유효 시간이다
const DefaultLeaseTTL = 60 * time.Minute

var (
	// ErrTermMismatch 는 expected_term 이 현재 지휘권 임기 번호와 다를 때 반환한다
	ErrTermMismatch = errors.New("지휘권 임기 번호(expected_term) 불일치")
	// ErrLeaseActive 는 활성 리스가 아직 유효해 force 없이 승계할 수 없을 때 반환한다
	ErrLeaseActive = errors.New("현재 지휘자의 리스가 아직 만료되지 않음")
	// ErrLeaseExpired 는 만료된 리스를 renew·handoff 하려 할 때 반환한다
	ErrLeaseExpired = errors.New("만료된 지휘권 리스는 갱신·이양할 수 없음 (claim 필요)")
	// ErrLeaseHandedOff 는 개방 이양된 리스를 다시 renew·handoff 하려 할 때 반환한다
	ErrLeaseHandedOff = errors.New("개방 이양 상태의 리스는 갱신·재이양할 수 없음")
	// ErrNotCoordinator 는 현재 지휘자가 아닌 에이전트가 갱신/이양을 시도할 때 반환한다
	ErrNotCoordinator = errors.New("현재 지휘자만 리스를 갱신·이양할 수 있음")
	// ErrReasonRequired 는 활성 리스를 force 로 강제 승계할 때 사유가 비어 있으면 반환한다
	ErrReasonRequired = errors.New("활성 리스의 강제 승계(force)에는 사유(reason)가 필요함")
	// ErrInvalidAgentID 는 에이전트 ID가 소문자 slug 규격에 맞지 않을 때 반환한다
	ErrInvalidAgentID = errors.New("에이전트 ID는 ^[a-z0-9][a-z0-9-]{0,23}$ 형식이어야 함")
)

// CoordinatorLease 는 Split-Brain 없는 단일 오케스트레이터 지휘권 리스다
type CoordinatorLease struct {
	Leader         string   `json:"leader"`
	Term           uint64   `json:"term"`
	LeaseUntil     float64  `json:"lease_until"`
	UpdatedAt      float64  `json:"updated_at"`
	PreviousLeader string   `json:"previous_leader,omitempty"`
	Reason         string   `json:"reason,omitempty"`
	ActiveAgents   []string `json:"active_agents,omitempty"`
	HandedOffTo    string   `json:"handed_off_to,omitempty"`
}

// GetCoordinator 는 coord.json에 기록된 오케스트레이터 지휘 리스를 반환한다(없으면 nil).
func (b *Board) GetCoordinator() (*CoordinatorLease, error) {
	if err := b.ensureCoordMigrated(); err != nil {
		return nil, err
	}
	var out *CoordinatorLease
	err := b.withCoordLock(func() error {
		lease, _, err := b.loadCoordFile()
		if err != nil {
			return err
		}
		out = cloneCoordinator(lease)
		return nil
	})
	return out, err
}

// ClaimCoordinator 는 expectedTerm CAS 를 확인한 뒤 agentID 에게 새 지휘권 리스(term+1)를 부여한다.
// 활성 리스가 남아 있으면 만료·이양 상태거나 force=true(사유 필수)인 경우에만 승계를 허용한다.
func (b *Board) ClaimCoordinator(agentID string, expectedTerm uint64, ttl time.Duration, reason string, activeAgents []string, force bool) (CoordinatorLease, error) {
	if !isValidSlug(agentID) {
		return CoordinatorLease{}, fmt.Errorf("%q: %w", agentID, ErrInvalidAgentID)
	}
	if ttl <= 0 {
		ttl = DefaultLeaseTTL
	}
	if err := b.ensureCoordMigrated(); err != nil {
		return CoordinatorLease{}, err
	}
	var out CoordinatorLease
	err := b.withCoordLock(func() error {
		cur, _, err := b.loadCoordFile()
		if err != nil {
			return err
		}
		now := nowSeconds()
		prevLeader := ""
		var curTerm uint64
		if cur != nil {
			curTerm = cur.Term
			prevLeader = cur.Leader
			if err := canTakeover(cur, agentID, expectedTerm, now, reason, force); err != nil {
				return err
			}
		} else if expectedTerm != 0 {
			return fmt.Errorf("현재=0, 요청=%d: %w", expectedTerm, ErrTermMismatch)
		}
		next := CoordinatorLease{
			Leader:         agentID,
			Term:           curTerm + 1,
			LeaseUntil:     now + ttl.Seconds(),
			UpdatedAt:      now,
			PreviousLeader: prevLeader,
			Reason:         strings.TrimSpace(reason),
			ActiveAgents:   cleanAgents(activeAgents, agentID),
		}
		if err := b.saveCoordFile(&next); err != nil {
			return err
		}
		out = next
		return nil
	})
	return out, err
}

// RenewCoordinator 는 현 지휘자가 동일 term 으로 자신의 유효한 리스 만료 시각을 연장한다.
func (b *Board) RenewCoordinator(agentID string, expectedTerm uint64, ttl time.Duration) (CoordinatorLease, error) {
	if !isValidSlug(agentID) {
		return CoordinatorLease{}, fmt.Errorf("%q: %w", agentID, ErrInvalidAgentID)
	}
	if ttl <= 0 {
		ttl = DefaultLeaseTTL
	}
	if err := b.ensureCoordMigrated(); err != nil {
		return CoordinatorLease{}, err
	}
	var out CoordinatorLease
	err := b.withCoordLock(func() error {
		cur, _, err := b.loadCoordFile()
		if err != nil {
			return err
		}
		now := nowSeconds()
		if err := validateLeaderCAS(cur, agentID, expectedTerm, now); err != nil {
			return err
		}
		cur.LeaseUntil = now + ttl.Seconds()
		cur.UpdatedAt = now
		if err := b.saveCoordFile(cur); err != nil {
			return err
		}
		out = *cloneCoordinator(cur)
		return nil
	})
	return out, err
}

// HandoffCoordinator 는 현 지휘자가 유효한 리스를 정상 이양한다.
// toAgent 가 지정되면 즉시 해당 에이전트로 term+1 리스를 넘기고, 비어 있으면 개방 이양("*")으로 만료시킨다.
func (b *Board) HandoffCoordinator(fromAgent, toAgent string, expectedTerm uint64, reason string, ttl time.Duration) (CoordinatorLease, error) {
	if !isValidSlug(fromAgent) {
		return CoordinatorLease{}, fmt.Errorf("%q: %w", fromAgent, ErrInvalidAgentID)
	}
	if toAgent != "" && !isValidSlug(toAgent) {
		return CoordinatorLease{}, fmt.Errorf("%q: %w", toAgent, ErrInvalidAgentID)
	}
	if ttl <= 0 {
		ttl = DefaultLeaseTTL
	}
	if err := b.ensureCoordMigrated(); err != nil {
		return CoordinatorLease{}, err
	}
	var out CoordinatorLease
	err := b.withCoordLock(func() error {
		cur, _, err := b.loadCoordFile()
		if err != nil {
			return err
		}
		now := nowSeconds()
		if err := validateLeaderCAS(cur, fromAgent, expectedTerm, now); err != nil {
			return err
		}
		var next CoordinatorLease
		if toAgent != "" {
			next = CoordinatorLease{
				Leader:         toAgent,
				Term:           cur.Term + 1,
				LeaseUntil:     now + ttl.Seconds(),
				UpdatedAt:      now,
				PreviousLeader: fromAgent,
				Reason:         strings.TrimSpace(reason),
				ActiveAgents:   cleanAgents(cur.ActiveAgents, toAgent),
			}
		} else {
			next = CoordinatorLease{
				Leader:         fromAgent,
				Term:           cur.Term,
				LeaseUntil:     0,
				UpdatedAt:      now,
				PreviousLeader: cur.PreviousLeader,
				Reason:         strings.TrimSpace(reason),
				ActiveAgents:   slices.Clone(cur.ActiveAgents),
				HandedOffTo:    "*",
			}
		}
		if err := b.saveCoordFile(&next); err != nil {
			return err
		}
		out = next
		return nil
	})
	return out, err
}

// CoordinatorStatus 는 리스 상태를 vacant, active, expired, handed_off 가운데 하나로 돌려준다.
func CoordinatorStatus(lease *CoordinatorLease, now time.Time) string {
	if lease == nil {
		return "vacant"
	}
	if lease.HandedOffTo != "" {
		return "handed_off"
	}
	if float64(now.UnixNano())/1e9 >= lease.LeaseUntil {
		return "expired"
	}
	return "active"
}

func validateLeaderCAS(cur *CoordinatorLease, agentID string, expectedTerm uint64, now float64) error {
	if cur == nil || cur.Leader != agentID {
		return fmt.Errorf("요청=%s: %w", agentID, ErrNotCoordinator)
	}
	if cur.Term != expectedTerm {
		return fmt.Errorf("현재=%d, 요청=%d: %w", cur.Term, expectedTerm, ErrTermMismatch)
	}
	if cur.HandedOffTo != "" {
		return fmt.Errorf("leader=%s, term=%d: %w", cur.Leader, cur.Term, ErrLeaseHandedOff)
	}
	if now >= cur.LeaseUntil {
		return fmt.Errorf("leader=%s, term=%d: %w", cur.Leader, cur.Term, ErrLeaseExpired)
	}
	return nil
}

func canTakeover(cur *CoordinatorLease, agentID string, expectedTerm uint64, now float64, reason string, force bool) error {
	if cur.Term != expectedTerm {
		return fmt.Errorf("현재=%d, 요청=%d: %w", cur.Term, expectedTerm, ErrTermMismatch)
	}
	if cur.Leader == agentID || cur.HandedOffTo == "*" || cur.HandedOffTo == agentID || now >= cur.LeaseUntil {
		return nil
	}
	if !force {
		return fmt.Errorf("leader=%s, term=%d: %w", cur.Leader, cur.Term, ErrLeaseActive)
	}
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("leader=%s, term=%d: %w", cur.Leader, cur.Term, ErrReasonRequired)
	}
	return nil
}

func cleanAgents(agents []string, include string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, a := range agents {
		if isValidSlug(a) && !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	if isValidSlug(include) && !seen[include] {
		out = append(out, include)
	}
	return out
}

func isValidSlug(id string) bool {
	if len(id) == 0 || len(id) > 24 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		alnum := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
		if i == 0 && !alnum {
			return false
		}
		if !alnum && c != '-' {
			return false
		}
	}
	return true
}
