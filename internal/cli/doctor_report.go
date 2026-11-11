package cli

type DoctorFinding struct {
	Code              string `json:"code"`
	Severity          string `json:"severity"`
	App               string `json:"app,omitempty"`
	Target            string `json:"target,omitempty"`
	Path              string `json:"path,omitempty"`
	Message           string `json:"message"`
	RecommendedAction string `json:"recommended_action"`
}

type RecoveryOperation struct {
	OperationID        string   `json:"operation_id,omitempty"`
	Type               string   `json:"operation_type,omitempty"`
	App                string   `json:"app"`
	Target             string   `json:"target"`
	Source             string   `json:"source,omitempty"`
	Destination        string   `json:"destination,omitempty"`
	JournalState       string   `json:"journal_state"`
	ManifestState      string   `json:"manifest_state"`
	LinkState          string   `json:"link_state"`
	ResolvedLinkTarget string   `json:"resolved_link_target,omitempty"`
	BackupID           string   `json:"backup_id,omitempty"`
	BackupState        string   `json:"backup_state,omitempty"`
	BackupVerification string   `json:"backup_verification"`
	ResumeAvailable    bool     `json:"resume_available"`
	ResumeBlockers     []string `json:"resume_blockers"`
	FilesystemChecks   []string `json:"filesystem_checks"`
	CompletedActions   []string `json:"completed_actions"`
	PendingActions     []string `json:"pending_actions"`
	SafeNextActions    []string `json:"safe_next_actions"`
	Uncertainties      []string `json:"uncertainties"`
}

type DoctorReport struct {
	OK         bool                `json:"ok"`
	Findings   []DoctorFinding     `json:"findings"`
	Operations []RecoveryOperation `json:"operations"`
	Issues     []string            `json:"issues"`
}
