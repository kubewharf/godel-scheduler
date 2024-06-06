package rescheduling

import (
	"fmt"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

type ownerRecommondations struct {
	// nodeName->MovementDetailOnNode
	nodeToMovements map[string][]*framework.MovementDetailOnNode
	waitingPodCount int
}

func newOwnerRecommondations() *ownerRecommondations {
	return &ownerRecommondations{
		nodeToMovements: make(map[string][]*framework.MovementDetailOnNode),
	}
}

type unitState struct {
	// ownerKey->ownerRecommondations
	index           map[string]*ownerRecommondations
	choosedMovement string
	algorithmName   string
}

func getUnitState(cycleState *framework.CycleState) (*unitState, error) {
	c, err := cycleState.Read(unitStateKey)
	if err != nil {
		return nil, fmt.Errorf("error reading %q from unit cycleState: %v", unitStateKey, err)
	}

	s, ok := c.(*unitState)
	if !ok {
		return nil, fmt.Errorf("%+v convert to Rescheduling.unitState error", c)
	}
	return s, nil
}

func (state *unitState) Clone() framework.StateData {
	// Do not clone for Rescheduling.
	return state
}
