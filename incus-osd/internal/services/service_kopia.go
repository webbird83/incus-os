package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lxc/incus/v6/shared/subprocess"

	"github.com/lxc/incus-os/incus-osd/api"
	"github.com/lxc/incus-os/incus-osd/internal/applications"
	"github.com/lxc/incus-os/incus-osd/internal/state"
	"github.com/lxc/incus-os/incus-osd/internal/storage"
	"github.com/lxc/incus-os/incus-osd/internal/zfs"
)

const (
	// kopiaCacheDir is the directory path for the Kopia cache.
	kopiaCacheDir = "/var/lib/incus-os/kopia"
)

// Kopia represents the system Kopia backup service.
type Kopia struct {
	common

	state *state.State
}

// Get returns the current service state.
func (n *Kopia) Get(ctx context.Context) (any, error) {
	// Refresh available snapshots if repository is connected.
	if n.state.Services.Kopia.Config.Enabled && n.state.Services.Kopia.State.RepositoryConnected {
		err := n.refreshSnapshots(ctx)
		if err != nil {
			// Log error but don't fail the Get operation.
			slog.WarnContext(ctx, "Failed to refresh snapshots", "err", err)
		}
	}

	return n.state.Services.Kopia, nil
}

// Update updates the service configuration.
func (n *Kopia) Update(ctx context.Context, req any) error {
	newState, ok := req.(*api.ServiceKopia)
	if !ok {
		return fmt.Errorf("request type \"%T\" isn't expected ServiceKopia", req)
	}

	oldState := n.state.Services.Kopia

	// Save the state on return.
	defer n.state.Save()

	// Disable the service if requested.
	if oldState.Config.Enabled && !newState.Config.Enabled {
		err := n.Stop(ctx)
		if err != nil {
			return err
		}
	}

	// Check for restore trigger before updating configuration.
	if newState.Config.RestoreSnapshotID != "" && newState.Config.RestoreSnapshotID != oldState.Config.RestoreSnapshotID {
		// Perform restore operation.
		err := n.PerformRestore(ctx, newState.Config.RestoreSnapshotID)
		if err != nil {
			return err
		}

		// Clear the restore field after successful restore.
		newState.Config.RestoreSnapshotID = ""
	}

	// Update the configuration.
	n.state.Services.Kopia.Config = newState.Config

	// Enable the service if requested.
	if !oldState.Config.Enabled && newState.Config.Enabled {
		err := n.Start(ctx)
		if err != nil {
			return err
		}
	}

	// Configure the service if enabled.
	if n.state.Services.Kopia.Config.Enabled {
		err := n.configure(ctx)
		if err != nil {
			return err
		}
	}

	return nil
}

// Stop stops the service.
func (n *Kopia) Stop(ctx context.Context) error {
	if !n.state.Services.Kopia.Config.Enabled {
		return nil
	}

	// Mark as not in progress.
	n.state.Services.Kopia.State.InProgress = false
	n.state.Services.Kopia.State.Progress = 0

	return nil
}

// Start starts the service.
func (n *Kopia) Start(ctx context.Context) error {
	if !n.state.Services.Kopia.Config.Enabled {
		return nil
	}

	// Configure the service.
	return n.configure(ctx)
}

// ShouldStart returns true if the service should be started on boot.
func (n *Kopia) ShouldStart() bool {
	return n.state.Services.Kopia.Config.Enabled
}

// Struct returns the API struct for the Kopia service.
func (*Kopia) Struct() any {
	return &api.ServiceKopia{}
}

// Supported returns whether the system can use Kopia.
func (n *Kopia) Supported() bool {
	// Check if kopia command is available.
	_, err := subprocess.RunCommand("which", "kopia")
	return err == nil
}

// configure takes care of configuring the Kopia repository connection.
// It will automatically connect to an existing repository or create a new one if it doesn't exist.
func (n *Kopia) configure(ctx context.Context) error {
	config := n.state.Services.Kopia.Config

	// Validate backend configuration.
	err := n.validateBackendConfig(config.Backend)
	if err != nil {
		n.state.Services.Kopia.State.RepositoryConnected = false
		n.state.Services.Kopia.State.LastStatus = "Backend configuration invalid: " + err.Error()

		return err
	}

	// Ensure Kopia cache dataset exists on ZFS pool.
	err = n.ensureKopiaCacheDataset(ctx)
	if err != nil {
		return fmt.Errorf("failed to ensure Kopia cache dataset: %w", err)
	}

	// Set KOPIA_CACHE_DIRECTORY environment variable.
	// The directory will be automatically mounted by ZFS.
	os.Setenv("KOPIA_CACHE_DIRECTORY", kopiaCacheDir)

	// Try to connect to existing repository first.
	err = n.connectOrInitRepository(ctx, config.Backend)
	if err != nil {
		n.state.Services.Kopia.State.RepositoryConnected = false
		n.state.Services.Kopia.State.LastStatus = "Failed to connect or initialize repository: " + err.Error()

		return err
	}

	n.state.Services.Kopia.State.RepositoryConnected = true
	n.state.Services.Kopia.State.LastStatus = "Repository connected"

	return nil
}

// ensureKopiaCacheDataset ensures that the ZFS dataset for Kopia cache exists and is properly mounted.
func (n *Kopia) ensureKopiaCacheDataset(ctx context.Context) error {
	const datasetName = "local/kopia-cache"
	mountpoint := kopiaCacheDir

	// Check if dataset already exists.
	if storage.DatasetExists(ctx, datasetName) {
		slog.DebugContext(ctx, "Kopia cache dataset already exists", "dataset", datasetName)
		return nil
	}

	// Create the dataset with the specified mountpoint.
	slog.InfoContext(ctx, "Creating Kopia cache dataset", "dataset", datasetName, "mountpoint", mountpoint)
	properties := map[string]string{
		"mountpoint": mountpoint,
		"canmount":   "on",
	}

	err := zfs.CreateDataset(ctx, "local", "kopia-cache", properties)
	if err != nil {
		return fmt.Errorf("failed to create Kopia cache dataset: %w", err)
	}

	slog.InfoContext(ctx, "Kopia cache dataset created successfully", "dataset", datasetName)

	return nil
}

// validateBackendConfig validates the backend configuration.
func (n *Kopia) validateBackendConfig(backend api.ServiceKopiaBackendConfig) error {
	switch backend.Type {
	case "s3":
		if backend.S3 == nil {
			return errors.New("S3 backend configuration missing")
		}

		if backend.S3.Endpoint == "" || backend.S3.Bucket == "" || backend.S3.AccessKey == "" || backend.S3.SecretKey == "" {
			return errors.New("S3 configuration incomplete")
		}

		return nil
	default:
		return fmt.Errorf("unsupported backend type: %s", backend.Type)
	}
}

// connectOrInitRepository connects to an existing repository or creates a new one if it doesn't exist.
func (n *Kopia) connectOrInitRepository(ctx context.Context, backend api.ServiceKopiaBackendConfig) error {
	// First, try to connect to existing repository.
	err := n.connectRepository(ctx, backend)
	if err == nil {
		// Successfully connected to existing repository.
		return nil
	}

	// If connection failed, try to create a new repository.
	slog.InfoContext(ctx, "Repository not found, creating new one")

	return n.initRepository(ctx, backend)
}

// initRepository initializes a new Kopia repository.
func (n *Kopia) initRepository(ctx context.Context, backend api.ServiceKopiaBackendConfig) error {
	switch backend.Type {
	case "s3":
		return n.initS3Repository(ctx, backend.S3)
	default:
		return fmt.Errorf("unsupported backend type for init: %s", backend.Type)
	}
}

// initS3Repository initializes a new S3 Kopia repository.
func (n *Kopia) initS3Repository(ctx context.Context, s3Config *api.ServiceKopiaBackendS3) error {
	config := n.state.Services.Kopia.Config

	if config.RepositoryPassword == "" {
		return errors.New("repository_password is required for repository initialization")
	}

	// Build kopia repository create command.
	args := []string{
		"repository", "create", "s3",
		"--bucket", s3Config.Bucket,
		"--endpoint", s3Config.Endpoint,
		"--disable-tls",
		"--access-key", s3Config.AccessKey,
		"--secret-access-key", s3Config.SecretKey,
		"--password", config.RepositoryPassword,
	}

	if s3Config.Region != "" {
		args = append(args, "--region", s3Config.Region)
	}

	// Run kopia repository create.
	_, err := subprocess.RunCommandContext(ctx, "kopia", args...)
	if err != nil {
		return fmt.Errorf("failed to create repository: %w", err)
	}

	return nil
}

// connectRepository connects to an existing Kopia repository.
func (n *Kopia) connectRepository(ctx context.Context, backend api.ServiceKopiaBackendConfig) error {
	switch backend.Type {
	case "s3":
		return n.connectS3Repository(ctx, backend.S3)
	default:
		return fmt.Errorf("unsupported backend type for connect: %s", backend.Type)
	}
}

// connectS3Repository connects to an existing S3 Kopia repository.
func (n *Kopia) connectS3Repository(ctx context.Context, s3Config *api.ServiceKopiaBackendS3) error {
	config := n.state.Services.Kopia.Config

	if config.RepositoryPassword == "" {
		return errors.New("repository_password is required for repository connection")
	}

	// Build kopia repository connect command.
	args := []string{
		"repository", "connect", "s3",
		"--bucket", s3Config.Bucket,
		"--endpoint", s3Config.Endpoint,
		"--disable-tls",
		"--access-key", s3Config.AccessKey,
		"--secret-access-key", s3Config.SecretKey,
		"--password", config.RepositoryPassword,
	}

	if s3Config.Region != "" {
		args = append(args, "--region", s3Config.Region)
	}

	// Run kopia repository connect.
	_, err := subprocess.RunCommandContext(ctx, "kopia", args...)
	if err != nil {
		return fmt.Errorf("failed to connect repository: %w", err)
	}

	return nil
}

// refreshSnapshots refreshes the list of available snapshots from the repository.
func (n *Kopia) refreshSnapshots(ctx context.Context) error {
	// List snapshots using kopia snapshot list.
	output, err := subprocess.RunCommandContext(ctx, "kopia", "snapshot", "list", "--json")
	if err != nil {
		return fmt.Errorf("failed to list snapshots: %w", err)
	}

	// Parse JSON output.
	var snapshots []struct {
		ID     string `json:"id"`
		Source struct {
			Path string `json:"path"`
		} `json:"source"`
		StartTime   time.Time `json:"startTime"`
		Description string    `json:"description"`
		Stats       struct {
			TotalSize int64 `json:"totalSize"`
		} `json:"stats"`
	}

	err = json.Unmarshal([]byte(output), &snapshots)
	if err != nil {
		return fmt.Errorf("failed to parse snapshot list: %w", err)
	}

	// Convert to API format.
	apiSnapshots := make([]api.ServiceKopiaSnapshotInfo, 0, len(snapshots))
	for _, snap := range snapshots {
		apiSnapshots = append(apiSnapshots, api.ServiceKopiaSnapshotInfo{
			ID:          snap.ID,
			Time:        snap.StartTime,
			Size:        snap.Stats.TotalSize,
			Source:      snap.Source.Path,
			Description: snap.Description,
		})
	}

	n.state.Services.Kopia.State.AvailableSnapshots = apiSnapshots

	return nil
}

// findLocalPool finds the "local" ZFS pool.
func (n *Kopia) findLocalPool(ctx context.Context) (string, error) {
	// Check if "local" pool exists.
	if !storage.PoolExists(ctx, "local") {
		return "", errors.New("local ZFS pool not found")
	}

	return "local", nil
}

// getPoolMountpoint gets the mountpoint of a ZFS pool or dataset.
func (n *Kopia) getPoolMountpoint(ctx context.Context, poolName string) (string, error) {
	// Get mountpoint using zfs get.
	output, err := subprocess.RunCommandContext(ctx, "zfs", "get", "-H", "-o", "value", "mountpoint", poolName)
	if err != nil {
		return "", fmt.Errorf("failed to get mountpoint: %w", err)
	}

	mountpoint := strings.TrimSpace(output)
	if mountpoint == "none" || mountpoint == "legacy" {
		// For pools with no mountpoint, use the default location.
		return filepath.Join("/", poolName), nil
	}

	return mountpoint, nil
}

// createZFSSnapshot creates a ZFS snapshot for backup.
func (n *Kopia) createZFSSnapshot(ctx context.Context, poolName string) (string, error) {
	snapshotName := poolName + "@kopia-" + time.Now().Format("20060102-150405")

	// Create snapshot.
	_, err := subprocess.RunCommandContext(ctx, "zfs", "snapshot", snapshotName)
	if err != nil {
		return "", fmt.Errorf("failed to create ZFS snapshot: %w", err)
	}

	return snapshotName, nil
}

// destroyZFSSnapshot destroys a ZFS snapshot.
func (n *Kopia) destroyZFSSnapshot(ctx context.Context, snapshotName string) error {
	_, err := subprocess.RunCommandContext(ctx, "zfs", "destroy", snapshotName)
	if err != nil {
		return fmt.Errorf("failed to destroy ZFS snapshot: %w", err)
	}

	return nil
}

// getSnapshotPath gets the path to access a ZFS snapshot.
func (n *Kopia) getSnapshotPath(ctx context.Context, snapshotName string) (string, error) {
	// Get the mountpoint of the base dataset.
	poolName := strings.Split(snapshotName, "@")[0]
	mountpoint, err := n.getPoolMountpoint(ctx, poolName)
	if err != nil {
		return "", err
	}

	// Snapshot is accessible via .zfs/snapshot/ directory.
	snapshotPath := filepath.Join(mountpoint, ".zfs", "snapshot", strings.Split(snapshotName, "@")[1])

	// Check if snapshot path exists.
	if _, err := os.Stat(snapshotPath); err != nil {
		return "", fmt.Errorf("snapshot path does not exist: %w", err)
	}

	return snapshotPath, nil
}

// isInMaintenanceWindow checks if we're currently in a maintenance window using SystemUpdate maintenance windows.
func (n *Kopia) isInMaintenanceWindow() bool {
	// Use SystemUpdate maintenance windows.
	updateConfig := n.state.System.Update.Config

	// If no maintenance windows are defined, allow backup at any time.
	if len(updateConfig.MaintenanceWindows) == 0 {
		return true
	}

	// Check if we're in any maintenance window.
	for _, window := range updateConfig.MaintenanceWindows {
		if window.IsCurrentlyActive() {
			return true
		}
	}

	return false
}

// performBackup performs a backup of the local ZFS pool.
func (n *Kopia) performBackup(ctx context.Context) error {
	// Check if repository is connected.
	if !n.state.Services.Kopia.State.RepositoryConnected {
		return errors.New("repository not connected")
	}

	// Check maintenance window.
	if !n.isInMaintenanceWindow() {
		n.state.Services.Kopia.State.LastStatus = "Outside maintenance window"
		return errors.New("outside maintenance window")
	}

	// Find local pool.
	poolName, err := n.findLocalPool(ctx)
	if err != nil {
		n.state.Services.Kopia.State.LastStatus = "Failed to find local pool: " + err.Error()
		return err
	}

	// Mark as in progress.
	n.state.Services.Kopia.State.InProgress = true
	n.state.Services.Kopia.State.Progress = 0
	n.state.Services.Kopia.State.LastStatus = "Creating ZFS snapshot"

	// Create ZFS snapshot.
	snapshotName, err := n.createZFSSnapshot(ctx, poolName)
	if err != nil {
		n.state.Services.Kopia.State.InProgress = false
		n.state.Services.Kopia.State.LastStatus = "Failed to create snapshot: " + err.Error()
		return err
	}

	// Cleanup snapshot on error.
	defer func() {
		if err != nil {
			_ = n.destroyZFSSnapshot(ctx, snapshotName)
		}
	}()

	// Get snapshot path.
	snapshotPath, err := n.getSnapshotPath(ctx, snapshotName)
	if err != nil {
		n.state.Services.Kopia.State.InProgress = false
		n.state.Services.Kopia.State.LastStatus = "Failed to get snapshot path: " + err.Error()
		return err
	}

	n.state.Services.Kopia.State.Progress = 25
	n.state.Services.Kopia.State.LastStatus = "Creating Kopia snapshot"

	// Create Kopia snapshot.
	description := fmt.Sprintf("Backup of %s pool at %s", poolName, time.Now().Format(time.RFC3339))
	args := []string{
		"snapshot", "create",
		snapshotPath,
		"--description", description,
	}

	_, err = subprocess.RunCommandContext(ctx, "kopia", args...)
	if err != nil {
		n.state.Services.Kopia.State.InProgress = false
		n.state.Services.Kopia.State.LastStatus = "Failed to create Kopia snapshot: " + err.Error()
		return err
	}

	n.state.Services.Kopia.State.Progress = 75
	n.state.Services.Kopia.State.LastStatus = "Applying retention policies"

	// Apply retention policies.
	err = n.applyRetention(ctx)
	if err != nil {
		slog.WarnContext(ctx, "Failed to apply retention policies", "err", err)
		// Don't fail the backup if retention fails.
	}

	n.state.Services.Kopia.State.Progress = 90
	n.state.Services.Kopia.State.LastStatus = "Cleaning up ZFS snapshot"

	// Destroy ZFS snapshot.
	err = n.destroyZFSSnapshot(ctx, snapshotName)
	if err != nil {
		slog.WarnContext(ctx, "Failed to destroy ZFS snapshot", "err", err)
		// Don't fail the backup if cleanup fails.
	}

	// Mark as complete.
	n.state.Services.Kopia.State.InProgress = false
	n.state.Services.Kopia.State.Progress = 100
	n.state.Services.Kopia.State.LastBackup = time.Now()
	n.state.Services.Kopia.State.LastStatus = "Backup completed successfully"

	return nil
}

// applyRetention applies Kopia-native retention policies to old snapshots.
func (n *Kopia) applyRetention(ctx context.Context) error {
	config := n.state.Services.Kopia.Config

	// Build retention policy arguments.
	args := []string{"snapshot", "expire"}

	if config.Retention.KeepLatest > 0 {
		args = append(args, "--keep-latest", fmt.Sprintf("%d", config.Retention.KeepLatest))
	}

	if config.Retention.KeepHourly > 0 {
		args = append(args, "--keep-hourly", fmt.Sprintf("%d", config.Retention.KeepHourly))
	}

	if config.Retention.KeepDaily > 0 {
		args = append(args, "--keep-daily", fmt.Sprintf("%d", config.Retention.KeepDaily))
	}

	if config.Retention.KeepWeekly > 0 {
		args = append(args, "--keep-weekly", fmt.Sprintf("%d", config.Retention.KeepWeekly))
	}

	if config.Retention.KeepMonthly > 0 {
		args = append(args, "--keep-monthly", fmt.Sprintf("%d", config.Retention.KeepMonthly))
	}

	if config.Retention.KeepAnnual > 0 {
		args = append(args, "--keep-annual", fmt.Sprintf("%d", config.Retention.KeepAnnual))
	}

	// If no retention policy is configured, don't run expire.
	if len(args) == 2 {
		return nil
	}

	// Run kopia snapshot expire.
	_, err := subprocess.RunCommandContext(ctx, "kopia", args...)
	if err != nil {
		return fmt.Errorf("failed to apply retention policy: %w", err)
	}

	return nil
}

// PerformRestore performs a full restore of the local ZFS pool from a Kopia snapshot.
// It stops all services, creates a safety snapshot, restores data, and restarts services.
func (n *Kopia) PerformRestore(ctx context.Context, snapshotID string) error {
	// Check if repository is connected.
	if !n.state.Services.Kopia.State.RepositoryConnected {
		return errors.New("repository not connected")
	}

	// Find local pool.
	poolName, err := n.findLocalPool(ctx)
	if err != nil {
		return err
	}

	n.state.Services.Kopia.State.InProgress = true
	n.state.Services.Kopia.State.Progress = 0
	n.state.Services.Kopia.State.LastStatus = "Stopping services"

	// Stop all applications.
	for appName, appInfo := range n.state.Applications {
		app, err := applications.Load(ctx, n.state, appName)
		if err != nil {
			slog.WarnContext(ctx, "Failed to load application for stop", "name", appName, "err", err)
			continue
		}

		slog.InfoContext(ctx, "Stopping application", "name", appName)
		err = app.Stop(ctx, appInfo.State.Version)
		if err != nil {
			slog.WarnContext(ctx, "Failed to stop application", "name", appName, "err", err)
		}
	}

	// Stop all services (reverse order from startup).
	serviceNames := slices.Clone(Supported(n.state))
	slices.Reverse(serviceNames)

	for _, srvName := range serviceNames {
		// Skip kopia service itself.
		if srvName == "kopia" {
			continue
		}

		srv, err := Load(ctx, n.state, srvName)
		if err != nil {
			slog.WarnContext(ctx, "Failed to load service for stop", "name", srvName, "err", err)
			continue
		}

		if !srv.ShouldStart() {
			continue
		}

		slog.InfoContext(ctx, "Stopping service", "name", srvName)
		err = srv.Stop(ctx)
		if err != nil {
			slog.WarnContext(ctx, "Failed to stop service", "name", srvName, "err", err)
		}
	}

	n.state.Services.Kopia.State.Progress = 20
	n.state.Services.Kopia.State.LastStatus = "Creating safety snapshot"

	// Create safety snapshot before restore.
	safetySnapshotName := poolName + "@before-restore-" + time.Now().Format("20060102-150405")
	_, err = n.createZFSSnapshot(ctx, safetySnapshotName)
	if err != nil {
		n.state.Services.Kopia.State.InProgress = false
		n.state.Services.Kopia.State.LastStatus = "Failed to create safety snapshot: " + err.Error()
		return fmt.Errorf("failed to create safety snapshot: %w", err)
	}

	n.state.Services.Kopia.State.Progress = 30
	n.state.Services.Kopia.State.LastStatus = "Preparing restore location"

	// Get pool mountpoint.
	mountpoint, err := n.getPoolMountpoint(ctx, poolName)
	if err != nil {
		n.state.Services.Kopia.State.InProgress = false
		n.state.Services.Kopia.State.LastStatus = "Failed to get pool mountpoint: " + err.Error()
		return err
	}

	n.state.Services.Kopia.State.Progress = 40
	n.state.Services.Kopia.State.LastStatus = "Clearing existing data"

	// Clear existing data (but keep the pool structure).
	// We need to be careful here - we should only clear datasets, not the pool itself.
	// For now, we'll restore to a temporary location and then move files.
	tempRestorePath := filepath.Join(mountpoint, ".kopia-restore-temp")
	err = os.MkdirAll(tempRestorePath, 0o700)
	if err != nil {
		n.state.Services.Kopia.State.InProgress = false
		n.state.Services.Kopia.State.LastStatus = "Failed to create temp restore path: " + err.Error()
		return err
	}

	// Cleanup temp path on exit.
	defer func() {
		_ = os.RemoveAll(tempRestorePath)
	}()

	n.state.Services.Kopia.State.Progress = 50
	n.state.Services.Kopia.State.LastStatus = "Restoring snapshot"

	// Restore snapshot to temporary location.
	err = n.restoreSnapshot(ctx, snapshotID, tempRestorePath)
	if err != nil {
		n.state.Services.Kopia.State.InProgress = false
		n.state.Services.Kopia.State.LastStatus = "Failed to restore snapshot: " + err.Error()
		return err
	}

	n.state.Services.Kopia.State.Progress = 70
	n.state.Services.Kopia.State.LastStatus = "Applying restored data"

	// Move restored data to actual location.
	// This is a simplified approach - in production, we might want to use ZFS send/receive.
	err = n.applyRestoredData(ctx, tempRestorePath, mountpoint)
	if err != nil {
		n.state.Services.Kopia.State.InProgress = false
		n.state.Services.Kopia.State.LastStatus = "Failed to apply restored data: " + err.Error()
		return err
	}

	n.state.Services.Kopia.State.Progress = 80
	n.state.Services.Kopia.State.LastStatus = "Starting services"

	// Start all services.
	for _, srvName := range Supported(n.state) {
		// Skip kopia service itself.
		if srvName == "kopia" {
			continue
		}

		srv, err := Load(ctx, n.state, srvName)
		if err != nil {
			slog.WarnContext(ctx, "Failed to load service for start", "name", srvName, "err", err)
			continue
		}

		if !srv.ShouldStart() {
			continue
		}

		slog.InfoContext(ctx, "Starting service", "name", srvName)
		err = srv.Start(ctx)
		if err != nil {
			slog.WarnContext(ctx, "Failed to start service", "name", srvName, "err", err)
		}
	}

	// Start all applications.
	for appName, appInfo := range n.state.Applications {
		app, err := applications.Load(ctx, n.state, appName)
		if err != nil {
			slog.WarnContext(ctx, "Failed to load application for start", "name", appName, "err", err)
			continue
		}

		slog.InfoContext(ctx, "Starting application", "name", appName)
		err = app.Start(ctx, appInfo.State.Version)
		if err != nil {
			slog.WarnContext(ctx, "Failed to start application", "name", appName, "err", err)
		}
	}

	// Mark as complete.
	n.state.Services.Kopia.State.InProgress = false
	n.state.Services.Kopia.State.Progress = 100
	n.state.Services.Kopia.State.LastStatus = "Restore completed successfully"

	return nil
}

// restoreSnapshot restores data from a Kopia snapshot to a target path.
func (n *Kopia) restoreSnapshot(ctx context.Context, snapshotID string, targetPath string) error {
	// Restore snapshot.
	args := []string{
		"snapshot", "restore",
		snapshotID,
		targetPath,
	}

	_, err := subprocess.RunCommandContext(ctx, "kopia", args...)
	if err != nil {
		return fmt.Errorf("failed to restore snapshot: %w", err)
	}

	return nil
}

// applyRestoredData applies restored data from temp location to actual mountpoint.
// This is a simplified approach - in a real implementation, we might want to use
// ZFS send/receive or more sophisticated data migration.
func (n *Kopia) applyRestoredData(ctx context.Context, tempPath string, mountpoint string) error {
	// For now, we'll use rsync to copy data.
	// In a production system, this might need to be more sophisticated.
	args := []string{
		"-a", "--delete",
		tempPath + "/",
		mountpoint + "/",
	}

	_, err := subprocess.RunCommandContext(ctx, "rsync", args...)
	if err != nil {
		return fmt.Errorf("failed to apply restored data: %w", err)
	}

	return nil
}
