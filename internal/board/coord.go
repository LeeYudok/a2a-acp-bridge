package board

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

// DefaultLeaseTTL 은 오케스트레이터 지휘 리스 기본 유효 시간이다
const DefaultLeaseTTL = 60 * time.Minute

var (
	// ErrTermMismatch 는 expected_term 이 현재 지휘권 임기 번호와 다를 때 반환한다
	ErrTermMismatch = errors.New("지휘권 임기 번호(expected_term) 불일치")
	// ErrLeaseActive 는 활성 리스가 아직 유효해 force 없이 승계할 수 없을 때 반환한다
	ErrLeaseActive = errors.New("현재 지휘자의 리스가 아직 만료되지 않음")
	// ErrNotCoordinator 는 현재 지휘자가 아닌 에이전트가 갱신/이양을 시도할 때 반환한다
	ErrNotCoordinator = errors.New("현재 지휘자만 리스를 갱신·이양할 수 있음")
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

// GetCoordinator 는 현재 보드에 기록된 오케스트레이터 지휘 리스를 반환한다(없으면 nil).
func (b *Board) GetCoordinator() (*CoordinatorLease, error) {
	var out *CoordinatorLease
	err := b.withLock(func() error {
		st, err := b.load()
		if err != nil {
			return err
		}
		if st.Coordinator != nil {
			copied := *st.Coordinator
			copied.ActiveAgents = slices.Clone(st.Coordinator.ActiveAgents)
			out = &copied
		}
		return nil
	})
	return out, err
}

// ClaimCoordinator 는 expectedTerm CAS 를 확인한 뒤 agentID 에게 새 지휘권 리스(term+1)를 부여한다.
// 활성 리스가 남아 있으면 만료·이양 상태거나 force=true 인 경우에만 승계를 허용한다.
func (b *Board) ClaimCoordinator(agentID string, expectedTerm uint64, ttl time.Duration, reason string, activeAgents []string, force bool) (CoordinatorLease, error) {
	if !isValidSlug(agentID) {
		return CoordinatorLease{}, fmt.Errorf("%q: %w", agentID, ErrInvalidAgentID)
	}
	if ttl <= 0 {
		ttl = DefaultLeaseTTL
	}
	var out CoordinatorLease
	err := b.withLock(func() error {
		st, err := b.load()
		if err != nil {
			return err
		}
		now := nowSeconds()
		prevLeader := ""
		var curTerm uint64
		if cur := st.Coordinator; cur != nil {
			curTerm = cur.Term
			prevLeader = cur.Leader
			if err := canTakeover(cur, agentID, expectedTerm, now, force); err != nil {
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
			Reason:         reason,
			ActiveAgents:   cleanAgents(activeAgents, agentID),
		}
		st.Coordinator = &next
		if err := b.save(st); err != nil {
			return err
		}
		out = next
		return nil
	})
	return out, err
}

// RenewCoordinator 는 현 지휘자가 동일 term 으로 자신의 리스 만료 시각을 연장한다.
func (b *Board) RenewCoordinator(agentID string, ttl time.Duration) (CoordinatorLease, error) {
	if !isValidSlug(agentID) {
		return CoordinatorLease{}, fmt.Errorf("%q: %w", agentID, ErrInvalidAgentID)
	}
	if ttl <= 0 {
		ttl = DefaultLeaseTTL
	}
	var out CoordinatorLease
	err := b.withLock(func() error {
		st, err := b.load()
		if err != nil {
			return err
		}
		if st.Coordinator == nil || st.Coordinator.Leader != agentID {
			return fmt.Errorf("요청=%s: %w", agentID, ErrNotCoordinator)
		}
		now := nowSeconds()
		st.Coordinator.LeaseUntil = now + ttl.Seconds()
		st.Coordinator.UpdatedAt = now
		st.Coordinator.HandedOffTo = ""
		if err := b.save(st); err != nil {
			return err
		}
		out = *st.Coordinator
		return nil
	})
	return out, err
}

// HandoffCoordinator 는 현 지휘자가 정상 이양한다.
// toAgent 가 지정되면 즉시 해당 에이전트로 term+1 리스를 넘기고, 비어 있으면 개방 이양("*")으로 만료시킨다.
func (b *Board) HandoffCoordinator(fromAgent, toAgent, reason string, ttl time.Duration) (CoordinatorLease, error) {
	if !isValidSlug(fromAgent) {
		return CoordinatorLease{}, fmt.Errorf("%q: %w", fromAgent, ErrInvalidAgentID)
	}
	if toAgent != "" && !isValidSlug(toAgent) {
		return CoordinatorLease{}, fmt.Errorf("%q: %w", toAgent, ErrInvalidAgentID)
	}
	if ttl <= 0 {
		ttl = DefaultLeaseTTL
	}
	var out CoordinatorLease
	err := b.withLock(func() error {
		st, err := b.load()
		if err != nil {
			return err
		}
		cur := st.Coordinator
		if cur == nil || cur.Leader != fromAgent {
			return fmt.Errorf("요청=%s: %w", fromAgent, ErrNotCoordinator)
		}
		now := nowSeconds()
		var next CoordinatorLease
		if toAgent != "" {
			next = CoordinatorLease{
				Leader:         toAgent,
				Term:           cur.Term + 1,
				LeaseUntil:     now + ttl.Seconds(),
				UpdatedAt:      now,
				PreviousLeader: fromAgent,
				Reason:         reason,
				ActiveAgents:   cleanAgents(cur.ActiveAgents, toAgent),
			}
		} else {
			next = CoordinatorLease{
				Leader:         fromAgent,
				Term:           cur.Term,
				LeaseUntil:     0,
				UpdatedAt:      now,
				PreviousLeader: cur.PreviousLeader,
				Reason:         reason,
				ActiveAgents:   slices.Clone(cur.ActiveAgents),
				HandedOffTo:    "*",
			}
		}
		st.Coordinator = &next
		if err := b.save(st); err != nil {
			return err
		}
		out = next
		return nil
	})
	return out, err
}

func canTakeover(cur *CoordinatorLease, agentID string, expectedTerm uint64, now float64, force bool) error {
	if cur.Term != expectedTerm {
		return fmt.Errorf("현재=%d, 요청=%d: %w", cur.Term, expectedTerm, ErrTermMismatch)
	}
	if force || cur.Leader == agentID || cur.HandedOffTo == "*" || cur.HandedOffTo == agentID || now >= cur.LeaseUntil {
		return nil
	}
	return fmt.Errorf("leader=%s, term=%d: %w", cur.Leader, cur.Term, ErrLeaseActive)
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
