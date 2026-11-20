package outlet

import "github.com/allanpk716/agent-taskboard/internal/outletstate"

const (
	StatusActive                 = outletstate.StatusActive
	StatusPaused                 = outletstate.StatusPaused
	DeliveryStateOK              = outletstate.DeliveryStateOK
	DeliveryStateTransient       = outletstate.DeliveryStateTransient
	DeliveryStateIgnored         = outletstate.DeliveryStateIgnored
	DeliveryStatePermanent       = outletstate.DeliveryStatePermanent
	PauseReasonMember            = outletstate.PauseReasonMember
	PauseReasonOutletPaused      = outletstate.PauseReasonOutletPaused
	PauseReasonMemberDeactivated = outletstate.PauseReasonMemberDeactivated
	PendingStateResetSQL         = outletstate.PendingStateResetSQL
	DeliveryStateResetSQL        = outletstate.DeliveryStateResetSQL
	LastPushedAtResetSQL         = outletstate.LastPushedAtResetSQL
)
