# wpback

**wpback** is a lightweight WordPress backup and restore tool written in Go.

It creates encrypted backup archives containing both:

- WordPress site files
- MySQL database dump

Backups are stored as encrypted `.tar.gz.enc` files using **AES-256-GCM** encryption.

`wpback` is designed to be simple, scriptable, and suitable for use on Linux servers, cron jobs, and systemd-based environments.



## Features

- One-command WordPress backup
- Encrypted backup archives using AES-256-GCM
- Includes both site files and MySQL database
- Restore backups with automatic safety rollback
- Automatic cleanup of old backups based on retention policy
- Configuration via custom `--config` path or default `wpback.json`
- Password support via local key file or environment variable
- Optional systemd service installation
- Simple command-line interface
- Suitable for automation and server environments



## Backup Format

Each backup is created as a compressed and encrypted archive:

```text
wpback-YYYYMMDD-HHMMSS.tar.gz.enc
```

The archive contains:

```text
site/
database.sql
```

Where:

- `site/` contains the WordPress files from the configured site directory
- `database.sql` contains the MySQL database dump

The final archive is compressed using gzip and encrypted using AES-GCM.



## Requirements

Before using `wpback`, make sure the following requirements are met:

- Go 1.20 or later, required only for building from source
- `mysqldump` must be available for database backups
- `mysql` must be available for database restore
- Read permission for the WordPress site directory
- Write permission for the backup directory
- Access to the MySQL database with sufficient privileges
- Linux is recommended for service installation with systemd

If `mysqldump` or `mysql` are not available in the system `PATH`, you can provide their full paths in the configuration file.



## Build

Build the binary using:

```bash
make build
```

After building, the `wpback` binary will be available according to your Makefile output configuration.



## Setup

Before creating encrypted backups, initialize a local key file:

```bash
./wpback setup
```

You will be prompted to enter a password.

This password is used to encrypt and decrypt backup archives. A local key file named `wpback.key` will be created next to the binary.

> Keep this key file safe. If you lose the password or key file, existing encrypted backups may not be recoverable.



## Configuration

By default, if `--config` is not provided, `wpback` looks for the configuration file next to the binary:

```text
./wpback.json
```

You can also provide a custom configuration path:

```bash
./wpback once --config /path/to/wpback.json
```



## Example Configuration

Create a `wpback.json` file:

```json
{
  "backup_dir": "/var/backups/wp",
  "site_dir": "/var/www/html",
  "db_name": "wordpress",
  "db_user": "wpuser",
  "db_pass": "secret",
  "db_host": "localhost",
  "keep_days": 7,
  "mysqldump_path": "mysqldump",
  "mysql_path": "mysql"
}
```

### Configuration Options

| Option | Description |
|||
| `backup_dir` | Directory where encrypted backups will be stored |
| `site_dir` | WordPress installation directory |
| `db_name` | MySQL database name |
| `db_user` | MySQL database username |
| `db_pass` | MySQL database password |
| `db_host` | MySQL host, for example `localhost` |
| `keep_days` | Number of days to keep old backups |
| `mysqldump_path` | Path to `mysqldump` binary |
| `mysql_path` | Path to `mysql` binary |



## Usage

### Create a Backup

Run a backup once:

```bash
./wpback once --config /path/to/wpback.json
```

Or use the default configuration file next to the binary:

```bash
./wpback once
```

This command creates an encrypted backup archive in the configured `backup_dir`.



### List Backups

List available backups:

```bash
./wpback ls
```

If you are using a custom configuration file:

```bash
./wpback ls --config /path/to/wpback.json
```



### Restore a Backup

Restore from a full backup path:

```bash
./wpback restore /path/to/backup.tar.gz.enc
```

Or restore by filename if the backup exists in the configured backup directory:

```bash
./wpback restore wpback-YYYYMMDD-HHMMSS.tar.gz.enc
```

During restore, `wpback` creates a safety backup of the current WordPress files and database before applying the selected backup.

If the restore process fails, `wpback` attempts to roll back automatically using the safety backup.



## Environment Password

By default, `wpback` uses the local `wpback.key` file created during setup.

For automation, CI/CD, cron jobs, or containerized environments, you can provide the password using an environment variable:

```bash
export WPBACK_PASSWORD="your-password"
./wpback once
```

If `WPBACK_PASSWORD` is set, it takes priority over the local key file.

This is useful when you do not want to store a key file on disk.



## Backup Retention

Old backups are automatically removed according to the configured `keep_days` value.

Example:

```json
{
  "keep_days": 7
}
```

With this setting, backups older than 7 days will be deleted automatically.

Set this value according to your storage capacity and recovery requirements.



## Restore Safety and Rollback

`wpback` is designed to make restores safer.

Before restoring a selected backup, it creates a safety backup of the current state, including:

- Current WordPress files
- Current MySQL database

If the restore process completes successfully, the selected backup becomes the active site state.

If the restore process fails, `wpback` attempts to automatically roll back to the safety backup.

This helps reduce the risk of ending up with a broken or partially restored WordPress installation.



## Service Installation on Linux

`wpback` can be installed as a systemd service on Linux systems.

### Install the Service

```bash
./wpback service-install
```

### Uninstall the Service

```bash
./wpback service-uninstall
```

### Check Service Status

```bash
systemctl status wpback
```

### Start, Stop, and Restart

```bash
systemctl start wpback
systemctl stop wpback
systemctl restart wpback
```

> Depending on your system configuration, you may need to run these commands with `sudo`.

Example:

```bash
sudo ./wpback service-install
sudo systemctl status wpback
```



## Security Notes

- Store `wpback.json` securely if it contains database credentials.
- Restrict file permissions for the configuration file:

```bash
chmod 600 wpback.json
```

- Protect the key file:

```bash
chmod 600 wpback.key
```

- Do not commit `wpback.json`, `wpback.key`, or backup files to version control.
- Store backup files in a secure location.
- Consider copying backups to remote storage for disaster recovery.
- Keep the encryption password safe. Without it, encrypted backups cannot be restored.



## Operational Notes

- Make sure the backup directory exists and is writable.
- Make sure the user running `wpback` can read the WordPress site directory.
- Make sure the MySQL user has permission to dump and restore the target database.
- For large WordPress installations, ensure enough temporary disk space is available.
- Test restore procedures regularly before relying on backups in production.



## Example Workflow

```bash
# Build the binary
make build

# Create encryption key
./wpback setup

# Create configuration file
nano wpback.json

# Run a backup
./wpback once

# List backups
./wpback ls

# Restore a backup
./wpback restore wpback-YYYYMMDD-HHMMSS.tar.gz.enc
```



## Recommended File Permissions

```bash
chmod 700 /var/backups/wp
chmod 600 wpback.json
chmod 600 wpback.key
```

If the binary is installed system-wide, make sure the service user has access only to the files and directories it needs.



## License

MIT License