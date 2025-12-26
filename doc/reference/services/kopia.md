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

* `restore_snapshot_id`: **Temporary one-time field.** Setting this field to a snapshot ID triggers a restore operation. The field is automatically cleared after the restore completes. To restore data, set this field via `incus admin os service edit kopia` and update the service configuration.

```{warning}
Restoring data will stop all services and applications, create a safety snapshot, restore the data, and restart all services and applications. This is a destructive operation.
```

## Maintenance windows

The Kopia service uses the system's maintenance windows configured in `system.update.config.maintenance_windows`. Backups will only be performed during active maintenance windows. If no maintenance windows are configured, backups can be performed at any time.

## State information

The service state includes:

* `repository_connected`: Whether the repository is currently connected
* `last_backup`: Timestamp of the last successful backup
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
2. Creates a ZFS snapshot of the local pool
3. Creates a Kopia snapshot from the ZFS snapshot
4. Applies retention policies
5. Cleans up the temporary ZFS snapshot

Backups are performed during maintenance windows as configured in the system update settings.
