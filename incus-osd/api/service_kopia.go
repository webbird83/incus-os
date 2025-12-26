package api

import (
	"time"
)

// ServiceKopiaSnapshotInfo represents information about an available snapshot for restore.
type ServiceKopiaSnapshotInfo struct {
	ID          string    `json:"id"          yaml:"id"`
	Time        time.Time `json:"time"        yaml:"time"`
	Size        int64     `json:"size"        yaml:"size"`
	Source      string    `json:"source"      yaml:"source"`
	Description string    `json:"description,omitempty" yaml:"description,omitempty"`
}

// ServiceKopiaRetentionPolicy represents Kopia retention policy configuration.
// These map to Kopia's native retention policy settings.
type ServiceKopiaRetentionPolicy struct {
	KeepLatest  int `json:"keep_latest,omitempty"  yaml:"keep_latest,omitempty"`   // Keep latest N snapshots
	KeepHourly  int `json:"keep_hourly,omitempty"  yaml:"keep_hourly,omitempty"`   // Keep N hourly snapshots
	KeepDaily   int `json:"keep_daily,omitempty"   yaml:"keep_daily,omitempty"`    // Keep N daily snapshots
	KeepWeekly  int `json:"keep_weekly,omitempty"  yaml:"keep_weekly,omitempty"`   // Keep N weekly snapshots
	KeepMonthly int `json:"keep_monthly,omitempty" yaml:"keep_monthly,omitempty"` // Keep N monthly snapshots
	KeepAnnual  int `json:"keep_annual,omitempty"  yaml:"keep_annual,omitempty"`  // Keep N annual snapshots
}

// ServiceKopiaBackendS3 represents S3-compatible backend configuration.
type ServiceKopiaBackendS3 struct {
	Endpoint  string `json:"endpoint"   yaml:"endpoint"`
	Bucket    string `json:"bucket"     yaml:"bucket"`
	AccessKey string `json:"access_key" yaml:"access_key"`
	SecretKey string `json:"secret_key" yaml:"secret_key"`
	Region    string `json:"region,omitempty" yaml:"region,omitempty"`
}

// ServiceKopiaBackendConfig represents the backend configuration.
// Only one backend type should be configured at a time.
// The structure is designed to support future backend types (e.g., SFTP).
type ServiceKopiaBackendConfig struct {
	Type string                `json:"type" yaml:"type"` // "s3", etc.
	S3   *ServiceKopiaBackendS3 `json:"s3,omitempty" yaml:"s3,omitempty"`
}

// ServiceKopiaConfig represents additional configuration for the Kopia service.
type ServiceKopiaConfig struct {
	Enabled            bool                      `json:"enabled"              yaml:"enabled"`
	RepositoryPassword string                    `json:"repository_password"   yaml:"repository_password"` // Required for encrypted repositories (both init and connect)
	Backend            ServiceKopiaBackendConfig `json:"backend"              yaml:"backend"`
	Retention          ServiceKopiaRetentionPolicy `json:"retention,omitempty" yaml:"retention,omitempty"`
	// RestoreSnapshotID is a temporary one-time field. Setting this triggers a restore operation.
	// The field is automatically cleared after the restore completes.
	RestoreSnapshotID  string                    `json:"restore_snapshot_id,omitempty" yaml:"restore_snapshot_id,omitempty"`
}

// ServiceKopiaState represents state for the Kopia service.
type ServiceKopiaState struct {
	RepositoryConnected bool                      `json:"repository_connected" yaml:"repository_connected"`
	LastBackup          time.Time                 `json:"last_backup"         yaml:"last_backup"`
	LastStatus          string                    `json:"last_status"         yaml:"last_status"`
	InProgress          bool                      `json:"in_progress"         yaml:"in_progress"`
	Progress            float64                   `json:"progress"            yaml:"progress"`
	AvailableSnapshots  []ServiceKopiaSnapshotInfo `json:"available_snapshots,omitempty" yaml:"available_snapshots,omitempty"`
}

// ServiceKopia represents the state and configuration of the Kopia service.
type ServiceKopia struct {
	State ServiceKopiaState `incusos:"-" json:"state" yaml:"state"`

	Config ServiceKopiaConfig `json:"config" yaml:"config"`
}
