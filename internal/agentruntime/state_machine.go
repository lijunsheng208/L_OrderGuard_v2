package agentruntime

import "fmt"

var transitions = map[Status]map[Status]bool{
	StatusCreated: {
		StatusPlanning: true, StatusCancelled: true,
	},
	StatusPlanning: {
		StatusInvestigating: true, StatusPlanningFailed: true,
		StatusInconclusive: true, StatusCancelled: true,
	},
	StatusInvestigating: {
		StatusKnowledgeLookup: true, StatusEvidenceCollected: true, StatusInvestigationFailed: true,
		StatusInconclusive: true, StatusCancelled: true,
	},
	StatusKnowledgeLookup: {StatusDiagnosing: true, StatusInconclusive: true, StatusCancelled: true},
	StatusDiagnosing:      {StatusCriticReview: true, StatusInconclusive: true, StatusCancelled: true},
	StatusCriticReview:    {StatusEvidenceCollected: true, StatusNoAnomaly: true, StatusInconclusive: true, StatusCancelled: true},
}

// ValidateTransition 校验阶段 3 工作流状态迁移。
func ValidateTransition(from, to Status) error {
	if transitions[from][to] {
		return nil
	}
	return fmt.Errorf("invalid investigation transition: %s -> %s", from, to)
}
