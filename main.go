package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/term"
)

const (
	appName         = "wpback"
	defaultKeepDays = 7
	passwordEnvKey  = "WPBACK_PASSWORD"
	keyFileName     = "wpback.key"
)

type Config struct {
	BackupDir     string `json:"backup_dir"`
	SiteDir       string `json:"site_dir"`
	DBName        string `json:"db_name"`
	DBUser        string `json:"db_user"`
	DBPass        string `json:"db_pass"`
	DBHost        string `json:"db_host"`
	KeepDays      int    `json:"keep_days"`
	MysqldumpPath string `json:"mysqldump_path"`
	MySQLPath     string `json:"mysql_path"`
}

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}

	switch os.Args[1] {
	case "setup":
		setupCmd(os.Args[2:])
	case "once":
		onceCmd(os.Args[2:])
	case "ls":
		lsCmd(os.Args[2:])
	case "restore":
		restoreCmd(os.Args[2:])
	case "service-install":
		serviceInstallCmd(os.Args[2:])
	case "service-uninstall":
		serviceUninstallCmd(os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	fmt.Println(`wpback - WordPress backup tool

Usage:
  wpback setup
  wpback once --config /path/config.json
  wpback ls --config /path/config.json
  wpback restore --config /path/config.json <backup-file>
  wpback service-install --config /path/config.json
  wpback service-uninstall
`)
}

// ---------- Setup ----------

func setupCmd(args []string) {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	force := fs.Bool("force", false, "overwrite existing key file")
	fs.Parse(args)

	keyPath, err := keyFilePath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot determine key path: %v\n", err)
		os.Exit(1)
	}

	if _, err := os.Stat(keyPath); err == nil && !*force {
		fmt.Fprintf(os.Stderr, "key file already exists: %s (use --force to overwrite)\n", keyPath)
		os.Exit(1)
	}

	pw := promptPassword()
	if err := writeKeyFile(keyPath, pw); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write key file: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Key file created:", keyPath)
}

// ---------- Commands ----------

func onceCmd(args []string) {
	fs := flag.NewFlagSet("once", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config.json")
	fs.Parse(args)

	cfg := mustLoadConfig(*configPath)
	pw := mustGetPassword()

	backupFile, err := runBackup(cfg, pw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Backup error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Backup created:", backupFile)

	if err := cleanupOld(cfg.BackupDir, cfg.KeepDays); err != nil {
		fmt.Fprintf(os.Stderr, "Cleanup error: %v\n", err)
	}
}

func lsCmd(args []string) {
	fs := flag.NewFlagSet("ls", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config.json")
	fs.Parse(args)

	cfg := mustLoadConfig(*configPath)
	files, _ := filepath.Glob(filepath.Join(cfg.BackupDir, "*.tar.gz.enc"))
	sort.Strings(files)
	for _, f := range files {
		fmt.Println(filepath.Base(f))
	}
}

func restoreCmd(args []string) {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config.json")
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "restore requires backup filename")
		os.Exit(1)
	}

	cfg := mustLoadConfig(*configPath)
	pw := mustGetPassword()
	backupFile := fs.Arg(0)

	if !strings.Contains(backupFile, "/") {
		backupFile = filepath.Join(cfg.BackupDir, backupFile)
	}

	if err := restoreBackup(cfg, pw, backupFile); err != nil {
		fmt.Fprintf(os.Stderr, "Restore error: %v\n", err)
		os.Exit(1)
	}
}

func serviceInstallCmd(args []string) {
	fmt.Println("service-install: use systemd/cron as previously instructed (not changed).")
}

func serviceUninstallCmd(args []string) {
	fmt.Println("service-uninstall: remove your cron/systemd entry.")
}

// ---------- Config ----------
func defaultConfigPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exe), "wpback.json"), nil
}

func mustLoadConfig(path string) *Config {
	if path == "" {
		if p, err := defaultConfigPath(); err == nil {
			path = p
		} else {
			fmt.Fprintln(os.Stderr, "config is required")
			os.Exit(1)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot read config: %v\n", err)
		os.Exit(1)
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "invalid config: %v\n", err)
		os.Exit(1)
	}
	if cfg.KeepDays == 0 {
		cfg.KeepDays = defaultKeepDays
	}
	if cfg.DBHost == "" {
		cfg.DBHost = "localhost"
	}
	if cfg.MysqldumpPath == "" {
		cfg.MysqldumpPath = "mysqldump"
	}
	if cfg.MySQLPath == "" {
		cfg.MySQLPath = "mysql"
	}
	return &cfg
}

// ---------- Password / Key file ----------

func keyFilePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(exe)
	return filepath.Join(dir, keyFileName), nil
}

func mustGetPassword() []byte {
	// 1) env (optional, for automation)
	if v := strings.TrimSpace(os.Getenv(passwordEnvKey)); v != "" {
		return []byte(v)
	}

	// 2) key file next to binary
	keyPath, err := keyFilePath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot determine key path: %v\n", err)
		os.Exit(1)
	}

	pw, err := readPasswordFile(keyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "password missing. run: wpback setup\n")
		os.Exit(1)
	}
	return pw
}

func readPasswordFile(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pw := strings.TrimSpace(string(b))
	if pw == "" {
		return nil, errors.New("key file is empty")
	}
	return []byte(pw), nil
}

func writeKeyFile(path string, pw []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, pw, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func promptPassword() []byte {
	fmt.Print("Set encryption password (input hidden): ")
	pw1, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		fmt.Fprintf(os.Stderr, "password input error: %v\n", err)
		os.Exit(1)
	}

	fmt.Print("Confirm encryption password: ")
	pw2, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		fmt.Fprintf(os.Stderr, "password input error: %v\n", err)
		os.Exit(1)
	}

	if !bytes.Equal(pw1, pw2) {
		fmt.Fprintln(os.Stderr, "passwords do not match")
		os.Exit(1)
	}
	if len(pw1) < 8 {
		fmt.Fprintln(os.Stderr, "password too short")
		os.Exit(1)
	}
	return pw1
}

// ---------- Backup ----------

func runBackup(cfg *Config, password []byte) (string, error) {
	ts := time.Now().Format("20060102-150405")
	base := fmt.Sprintf("wpback-%s", ts)

	tempDir, err := os.MkdirTemp("", "wpback-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "db.sql")
	if err := dumpDB(cfg, dbPath); err != nil {
		return "", err
	}

	tarGzPath := filepath.Join(tempDir, base+".tar.gz")
	if err := createTarGz(cfg.SiteDir, dbPath, tarGzPath); err != nil {
		return "", err
	}

	outFile := filepath.Join(cfg.BackupDir, base+".tar.gz.enc")
	if err := encryptFile(tarGzPath, outFile, password); err != nil {
		return "", err
	}

	return outFile, nil
}

func dumpDB(cfg *Config, outFile string) error {
	args := []string{
		"-h", cfg.DBHost,
		"-u", cfg.DBUser,
		fmt.Sprintf("-p%s", cfg.DBPass),
		cfg.DBName,
	}
	cmd := exec.Command(cfg.MysqldumpPath, args...)
	f, err := os.Create(outFile)
	if err != nil {
		return err
	}
	defer f.Close()
	cmd.Stdout = f
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func createTarGz(siteDir, dbFile, outFile string) error {
	f, err := os.Create(outFile)
	if err != nil {
		return err
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	if err := addFileToTar(tw, dbFile, "db.sql"); err != nil {
		return err
	}

	return filepath.Walk(siteDir, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(siteDir, path)
		if rel == "." {
			return nil
		}
		return addFileToTar(tw, path, rel)
	})
}

func addFileToTar(tw *tar.Writer, path, name string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	hdr.Name = name
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if info.Mode().IsRegular() {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := io.Copy(tw, f); err != nil {
			return err
		}
	}
	return nil
}

func encryptFile(inFile, outFile string, password []byte) error {
	plain, err := os.ReadFile(inFile)
	if err != nil {
		return err
	}
	key := sha256.Sum256(password)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	ciphertext := gcm.Seal(nonce, nonce, plain, nil)
	return os.WriteFile(outFile, ciphertext, 0600)
}

func decryptFile(inFile, outFile string, password []byte) error {
	ciphertext, err := os.ReadFile(inFile)
	if err != nil {
		return err
	}
	key := sha256.Sum256(password)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return errors.New("ciphertext too short")
	}
	nonce := ciphertext[:gcm.NonceSize()]
	data := ciphertext[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, data, nil)
	if err != nil {
		return err
	}
	return os.WriteFile(outFile, plain, 0600)
}

func cleanupOld(backupDir string, keepDays int) error {
	files, err := filepath.Glob(filepath.Join(backupDir, "*.tar.gz.enc"))
	if err != nil {
		return err
	}
	threshold := time.Now().AddDate(0, 0, -keepDays)
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		if info.ModTime().Before(threshold) {
			_ = os.Remove(f)
		}
	}
	return nil
}

// ---------- Restore (with safety rollback) ----------

func restoreBackup(cfg *Config, password []byte, backupFile string) error {
	tmpDir, err := os.MkdirTemp("", "wpback-restore-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	// safety backup
	safetyFile := filepath.Join("/tmp", "wpback-safety-"+time.Now().Format("20060102-150405")+".tar.gz.enc")
	if _, err := runBackup(cfg, password); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: safety backup failed: %v\n", err)
	} else {
		latest, _ := latestBackup(cfg.BackupDir)
		if latest != "" {
			_ = os.Rename(latest, safetyFile)
		}
	}

	tarGzPath := filepath.Join(tmpDir, "restore.tar.gz")
	if err := decryptFile(backupFile, tarGzPath, password); err != nil {
		return err
	}

	extractDir := filepath.Join(tmpDir, "extract")
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		return err
	}
	if err := extractTarGz(tarGzPath, extractDir); err != nil {
		return err
	}

	dbFile := filepath.Join(extractDir, "db.sql")
	if err := restoreDB(cfg, dbFile); err != nil {
		_ = rollbackRestore(cfg, password, safetyFile)
		return err
	}

	if err := restoreFiles(cfg.SiteDir, extractDir); err != nil {
		_ = rollbackRestore(cfg, password, safetyFile)
		return err
	}

	return nil
}

func latestBackup(dir string) (string, error) {
	files, _ := filepath.Glob(filepath.Join(dir, "*.tar.gz.enc"))
	if len(files) == 0 {
		return "", errors.New("no backups")
	}
	sort.Strings(files)
	return files[len(files)-1], nil
}

func restoreDB(cfg *Config, dbFile string) error {
	dropCreate := fmt.Sprintf("DROP DATABASE IF EXISTS `%s`; CREATE DATABASE `%s`;", cfg.DBName, cfg.DBName)
	cmd := exec.Command(cfg.MySQLPath, "-h", cfg.DBHost, "-u", cfg.DBUser, fmt.Sprintf("-p%s", cfg.DBPass), "-e", dropCreate)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	f, err := os.Open(dbFile)
	if err != nil {
		return err
	}
	defer f.Close()

	cmd = exec.Command(cfg.MySQLPath, "-h", cfg.DBHost, "-u", cfg.DBUser, fmt.Sprintf("-p%s", cfg.DBPass), cfg.DBName)
	cmd.Stdin = bufio.NewReader(f)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func extractTarGz(archive, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dest, hdr.Name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		default:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			f, err := os.Create(target)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}
	return nil
}

func restoreFiles(siteDir, extractDir string) error {
	entries, err := os.ReadDir(siteDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		_ = os.RemoveAll(filepath.Join(siteDir, e.Name()))
	}

	return filepath.Walk(extractDir, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(extractDir, path)
		if rel == "db.sql" || rel == "." {
			return nil
		}
		dest := filepath.Join(siteDir, rel)
		if info.IsDir() {
			return os.MkdirAll(dest, info.Mode())
		}
		return copyFile(path, dest, info.Mode())
	})
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}

func rollbackRestore(cfg *Config, password []byte, safetyFile string) error {
	fmt.Fprintln(os.Stderr, "Restore failed. Rolling back from safety backup...")
	return restoreBackup(cfg, password, safetyFile)
}
