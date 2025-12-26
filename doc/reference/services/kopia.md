# Kopia

The [Kopia](https://kopia.io/) service provides encrypted, deduplicated, and compressed backups of the local ZFS pool to S3-compatible storage.

## Configuration options

The full API structs for the service can be viewed [online](https://github.com/lxc/incus-os/blob/main/incus-osd/api/service_kopia.go).

The following configuration options can be set:

* `enabled`: If `true`, enable the Kopia backup service.

* `repository_password`: **Required.** The password for the encrypted Kopia repository. This password is required both for initializing a new repository and for connecting to an existing repository.

* `backend`: Backend configuration for the backup storage. Currently only S3-compatible storage is supported:
  * `type`: Backend type, currently only `"s3"` is supported.
  * `s3`: S3-compatible backend configuration:
    * `endpoint`: S3 endpoint URL (e.g., `https://s3.amazonaws.com` or `https://minio.example.com:9000`)
    * `bucket`: S3 bucket name
    * `access_key`: S3 access key ID
    * `secret_key`: S3 secret access key
    * `region`: S3 region (optional, some S3-compatible services don't require this)

* `retention`: Retention policy configuration using Kopia's native retention policies. All fields are optional:
  * `keep_latest`: Keep the latest N snapshots
  * `keep_hourly`: Keep N hourly snapshots
  * `keep_daily`: Keep N daily snapshots
  * `keep_weekly`: Keep N weekly snapshots
  * `keep_monthly`: Keep N monthly snapshots
  * `keep_annual`: Keep N annual snapshots

* `backup_frequency`: **Optional.** Defines the time interval between backup cycles. If not set or empty, defaults to once per maintenance window. Supported formats:
  * Empty string or not set: Once per maintenance window (default)
  * Duration string: Time interval between backups, e.g., `"1h"` for hourly, `"24h"` for daily, `"1w"` for weekly, `"2m"` for every 2 minutes

* `restore_snapshot_id`: **Temporary one-time field.** Setting this field to a snapshot ID triggers a restore operation. The field is automatically cleared after the restore completes. To restore data, set this field via `incus admin os service edit kopia` and update the service configuration.

```{warning}
Restoring data will stop all services and applications, create a safety snapshot, restore the data, and restart all services and applications. This is a destructive operation.
```

## Backup scheduling

By default, the Kopia service performs one backup per maintenance window. The system's maintenance windows are configured in `system.update.config.maintenance_windows`. If no maintenance windows are configured, backups can be performed at any time.

You can customize the backup frequency using the `backup_frequency` configuration option:

* **Default behavior** (empty or not set): One backup per maintenance window. The service tracks which maintenance window was used for the last backup and ensures only one backup is performed per window.

* **Duration string**: Specify the time interval between backups using a duration string, e.g., `"1h"` for hourly backups, `"24h"` for daily backups, `"1w"` for weekly backups, or `"2m"` for every 2 minutes. The service will perform a backup when the specified duration has elapsed since the last backup. Note: Backups with custom frequency will still only run during active maintenance windows (unless no maintenance windows are configured).

## State information

The service state includes:

* `repository_connected`: Whether the repository is currently connected
* `last_backup`: Timestamp of the last successful backup
* `last_backup_window`: Identifier for the maintenance window when the last backup was performed (only set when using default maintenance window schedule)
* `last_status`: Status message describing the current state
* `in_progress`: Whether a backup or restore operation is currently in progress
* `progress`: Progress percentage (0-100) for the current operation
* `available_snapshots`: List of available snapshots for restore, including:
  * `id`: Snapshot ID (use this for `restore_snapshot_id`)
  * `time`: Snapshot creation time
  * `size`: Snapshot size in bytes
  * `source`: Source path of the snapshot
  * `description`: Snapshot description

## Automatic backup behavior

When enabled, the service automatically:
1. Connects to the configured repository (or creates a new one if it doesn't exist)
2. Monitors the configured backup frequency (default: once per maintenance window)
3. When scheduled, creates a ZFS snapshot of the local pool
4. Creates a Kopia snapshot from the ZFS snapshot
5. Applies retention policies
6. Cleans up the temporary ZFS snapshot

The backup scheduler runs continuously and checks periodically if a backup should be performed based on the configured frequency. For default frequency (maintenance window), it checks every minute. For custom frequency, it checks at least every minute but only performs backups when the configured duration has elapsed. Backups are only performed during active maintenance windows (unless no maintenance windows are configured).
