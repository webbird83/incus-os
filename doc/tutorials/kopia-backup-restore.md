# Kopia Backup and Restore

This tutorial explains how to configure the Kopia backup service and perform backups and restores of your local ZFS pool data.

## Prerequisites

* An S3-compatible storage backend (e.g., AWS S3, MinIO, or Incus storage buckets)
* Access credentials for the S3 backend
* A repository password for encrypting backups

## Configuring the Kopia Service

### Step 1: Edit the service configuration

Edit the Kopia service configuration:

```
incus admin os service edit kopia
```

### Step 2: Configure the service

Add the following configuration:

```yaml
config:
  enabled: true
  repository_password: "your-secure-password-here"
  backend:
    type: s3
    s3:
      endpoint: "https://s3.amazonaws.com"
      bucket: "my-backup-bucket"
      access_key: "AKIAIOSFODNN7EXAMPLE"
      secret_key: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
      region: "us-east-1"
  backup_frequency: ""  # Optional: Default is once per maintenance window
  retention:
    keep_daily: 7
    keep_weekly: 4
    keep_monthly: 12
```

**Important notes:**

* `repository_password`: This password is required for both initializing a new repository and connecting to an existing one. Choose a strong password and store it securely.
* `backend.s3.endpoint`: For Incus storage buckets, use the bucket's S3 endpoint URL.
* `backend.s3.bucket`: The name of your S3 bucket or Incus storage bucket.
* `backup_frequency`: Optional. Defines the time interval between backup cycles. If not set or empty, defaults to once per maintenance window. You can use:
  * Duration string: `"1h"` for hourly backups, `"24h"` for daily backups, `"1w"` for weekly backups, `"2m"` for every 2 minutes
  * Note: Custom frequencies still respect maintenance windows - backups will only run during active maintenance windows (if one is defined).
* `retention`: Configure retention policies according to your needs. The example above keeps 7 daily, 4 weekly, and 12 monthly snapshots.

### Step 3: Verify the service status

Check that the service is connected and running:

```
incus admin os service show kopia
```

You should see:

```yaml
state:
  repository_connected: true
  last_status: "Repository connected"
```

## Automatic Backups

Once configured and enabled, the Kopia service automatically:

1. Connects to the repository (or creates a new one if it doesn't exist)
2. Monitors the configured backup frequency (default: once per maintenance window)
3. Performs backups according to the frequency during active maintenance windows
4. Applies retention policies after each backup

### Backup Frequency Configuration

By default, the service performs one backup per maintenance window. You can customize this using the `backup_frequency` option:

**Example: Hourly backups:**
```yaml
config:
  backup_frequency: "1h"
```

**Example: Daily backups:**
```yaml
config:
  backup_frequency: "24h"
```

**Example: Weekly backups:**
```yaml
config:
  backup_frequency: "1w"
```

Note: Even with custom frequencies, backups will only run during active maintenance windows (unless no maintenance windows are configured).

To check the status of recent backups:

```
incus admin os service show kopia
```

Look for `last_backup` timestamp and `available_snapshots` list.

## Restoring Data

### Step 1: List available snapshots

View available snapshots for restore:

```
incus admin os service show kopia
```

The output includes an `available_snapshots` array with snapshot information:

```yaml
state:
  available_snapshots:
  - id: "k1234567890abcdef"
    time: "2024-01-15T10:30:00Z"
    size: 1073741824
    source: "/local/.zfs/snapshot/kopia-20240115-103000"
    description: "Backup of local pool at 2024-01-15T10:30:00Z"
```

### Step 2: Trigger restore

To restore from a snapshot, edit the service configuration and set the `restore_snapshot_id` field:

```
incus admin os service edit kopia
```

Add or update the `restore_snapshot_id` field:

```yaml
config:
  enabled: true
  repository_password: "your-secure-password-here"
  restore_snapshot_id: "k1234567890abcdef"
  # ... rest of configuration
```

Save and close the editor. The restore operation will be triggered automatically.

```{warning}
The restore operation will:
1. Stop all services and applications
2. Create a safety snapshot named `local@before-restore-YYYYMMDD-HHMMSS`
3. Clear existing data
4. Restore data from the specified snapshot
5. Restart all services and applications

This is a destructive operation. Make sure you have selected the correct snapshot ID.
```

### Step 3: Monitor restore progress

Check the service status to monitor the restore progress:

```
incus admin os service show kopia
```

You should see:

```yaml
state:
  in_progress: true
  progress: 75.5
  last_status: "Restoring snapshot"
```

After the restore completes, the `restore_snapshot_id` field will be automatically cleared, and `in_progress` will be set to `false`.

### Step 4: Verify restore

After the restore completes, verify that your data has been restored correctly. The safety snapshot created before the restore is available in case you need to roll back:

```
zfs list -t snapshot | grep before-restore
```

## Using Incus Storage Buckets

Incus provides native S3-compatible storage buckets that can be used as a backup target. To use an Incus bucket:

1. Create a storage bucket in your Incus instance:

```
incus storage bucket create my-backup-bucket
```

2. Create an access key for the bucket:

```
incus storage bucket key create my-backup-bucket backup-key
```

3. Use the bucket's S3 endpoint and credentials in the Kopia service configuration:

```yaml
config:
  backend:
    type: s3
    s3:
      endpoint: "https://your-incus-instance:8443/storage/buckets/my-backup-bucket"
      bucket: "my-backup-bucket"
      access_key: "<access-key-from-bucket-key>"
      secret_key: "<secret-key-from-bucket-key>"
```

## Troubleshooting

### Repository connection fails

* Verify that `repository_password` is correct
* Check that S3 credentials are valid
* Ensure the S3 endpoint is accessible from the IncusOS system
* Verify the bucket exists and is accessible

### Backups not running

* Check that maintenance windows are configured in `system.update.config.maintenance_windows` (unless you have a custom frequency)
* Verify the service is enabled: `incus admin os service show kopia`
* Check that `repository_connected` is `true` in the service state
* Verify the backup frequency is correct (check `backup_frequency` configuration)
* Check the service status for error messages in `last_status`

### Restore fails

* Ensure the snapshot ID is valid (check `available_snapshots`)
* Verify that `repository_password` is correct
* Check system logs for detailed error messages
