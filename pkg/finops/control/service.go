// Package control contains the center-side FinOps optimization workflow.
package control

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	finopsmodel "github.com/gocrane/crane/pkg/finops/model"
	finopsv1 "github.com/gocrane/crane/pkg/finops/proto/v1"
	"github.com/gocrane/crane/pkg/finops/store"
	"github.com/gocrane/crane/pkg/optimization"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Service struct {
	Store store.Store
	Now   func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) CreatePlan(ctx context.Context, plan optimization.Plan) error {
	if s == nil || s.Store == nil {
		return errors.New("optimization control store is required")
	}
	return s.Store.PutOptimizationPlan(ctx, plan)
}

func (s *Service) ListPlans(ctx context.Context, identity finopsmodel.Identity) ([]optimization.Plan, error) {
	if s == nil || s.Store == nil {
		return nil, errors.New("optimization control store is required")
	}
	return s.Store.ListOptimizationPlans(ctx, identity)
}

func (s *Service) ApprovePlan(ctx context.Context, identity finopsmodel.Identity, planID string, revision uint64, approval optimization.Approval) (optimization.Plan, error) {
	if s == nil || s.Store == nil {
		return optimization.Plan{}, errors.New("optimization control store is required")
	}
	plan, err := s.Store.GetOptimizationPlan(ctx, identity, planID, revision)
	if err != nil {
		return optimization.Plan{}, err
	}
	if plan.State != optimization.StatePreview && plan.State != optimization.StateAwaitingApproval {
		return optimization.Plan{}, fmt.Errorf("plan state %s cannot be approved", plan.State)
	}
	if plan.State == optimization.StatePreview {
		if err := plan.Transition(optimization.StateAwaitingApproval); err != nil {
			return optimization.Plan{}, err
		}
	}
	if err := plan.Transition(optimization.StateApproved); err != nil {
		return optimization.Plan{}, err
	}
	if approval.ApprovedAt.IsZero() {
		approval.ApprovedAt = s.now()
	}
	if approval.ApprovedAt.After(plan.ExpiresAt) {
		return optimization.Plan{}, errors.New("approval is after plan expiry")
	}
	plan.Approval = &approval
	plan.Revision++
	if err := plan.Validate(); err != nil {
		return optimization.Plan{}, err
	}
	if err := s.Store.PutOptimizationPlan(ctx, plan); err != nil {
		return optimization.Plan{}, err
	}
	return plan, nil
}

func (s *Service) PublishPlans(ctx context.Context, identity finopsmodel.Identity, planIDs []string) (finopsmodel.DesiredState, error) {
	if s == nil || s.Store == nil {
		return finopsmodel.DesiredState{}, errors.New("optimization control store is required")
	}
	if err := identity.Validate(); err != nil {
		return finopsmodel.DesiredState{}, err
	}
	if len(planIDs) == 0 {
		return finopsmodel.DesiredState{}, errors.New("at least one plan is required")
	}
	ids := append([]string(nil), planIDs...)
	sort.Strings(ids)
	refs := make([]*finopsv1.PlanReference, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, planID := range ids {
		if _, exists := seen[planID]; exists || planID == "" {
			return finopsmodel.DesiredState{}, errors.New("plan IDs must be unique and non-empty")
		}
		seen[planID] = struct{}{}
		plan, err := s.Store.GetOptimizationPlan(ctx, identity, planID, 0)
		if err != nil {
			return finopsmodel.DesiredState{}, err
		}
		if plan.State != optimization.StateApproved {
			return finopsmodel.DesiredState{}, fmt.Errorf("plan %s is not approved", planID)
		}
		ref, err := plan.Reference()
		if err != nil {
			return finopsmodel.DesiredState{}, err
		}
		refs = append(refs, ref)
	}
	current, err := s.Store.GetDesiredState(ctx, identity)
	revision := uint64(1)
	if err == nil {
		revision = current.Revision + 1
	} else if !errors.Is(err, store.ErrNotFound) {
		return finopsmodel.DesiredState{}, err
	}
	snapshot := &finopsv1.DesiredStateSnapshot{Revision: revision, Plans: refs, CreatedAt: timestamppb.New(s.now())}
	if err := finopsv1.SetDesiredStateChecksum(snapshot); err != nil {
		return finopsmodel.DesiredState{}, err
	}
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(snapshot)
	if err != nil {
		return finopsmodel.DesiredState{}, err
	}
	var checksum [32]byte
	copy(checksum[:], snapshot.GetChecksum())
	state := finopsmodel.DesiredState{Identity: identity, Revision: revision, Checksum: checksum, Payload: payload, CreatedAt: s.now()}
	if err := s.Store.PutDesiredState(ctx, state); err != nil {
		return finopsmodel.DesiredState{}, err
	}
	return state, nil
}

func (s *Service) GetExecutionStatus(ctx context.Context, identity finopsmodel.Identity, planID string, revision uint64) (finopsmodel.ExecutionStatus, error) {
	if s == nil || s.Store == nil {
		return finopsmodel.ExecutionStatus{}, errors.New("optimization control store is required")
	}
	return s.Store.GetExecutionStatus(ctx, identity, planID, revision)
}
