package cmd

import (
	"os"
	"strings"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
)

var (
	backupIn   string
	backupOut  string
	backupFile string
)

var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Backup utilities (decrypt / verify)",
}

// backupDecryptCmd 解密加密备份, 口令取自 config.json backup.passphrase 或 OCTOPUS_BACKUP_PASSPHRASE。
var backupDecryptCmd = &cobra.Command{
	Use:   "decrypt",
	Short: "Decrypt an encrypted backup (.db.enc) into a plain SQLite file",
	PreRun: func(cmd *cobra.Command, args []string) {
		conf.Load(cfgFile)
	},
	Run: func(cmd *cobra.Command, args []string) {
		if backupIn == "" || backupOut == "" {
			log.Error("--in and --out are required")
			os.Exit(1)
		}
		passphrase := conf.AppConfig.Backup.Passphrase
		if strings.TrimSpace(passphrase) == "" {
			log.Error("backup passphrase is not configured (config.json backup.passphrase or OCTOPUS_BACKUP_PASSPHRASE)")
			os.Exit(1)
		}
		if err := op.DecryptBackupFile(backupIn, backupOut, passphrase); err != nil {
			log.Errorf("decrypt failed: %v", err)
			os.Exit(1)
		}
		log.Infof("decrypted %s -> %s", backupIn, backupOut)
	},
}

// backupVerifyCmd 校验备份: 有 .sha256 先验校验和; 明文 SQLite 再验关键表。
var backupVerifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Verify a backup file (checksum + SQLite integrity for plain backups)",
	Run: func(cmd *cobra.Command, args []string) {
		if backupFile == "" {
			log.Error("--file is required")
			os.Exit(1)
		}
		ok := true
		if _, err := os.Stat(op.BackupChecksumFile(backupFile)); err == nil {
			if op.VerifyBackupChecksum(backupFile) {
				log.Infof("checksum ok: %s", backupFile)
			} else {
				log.Errorf("checksum MISMATCH: %s", backupFile)
				ok = false
			}
		} else {
			log.Warnf("no checksum file for %s", backupFile)
		}
		if strings.HasSuffix(backupFile, ".enc") {
			log.Infof("encrypted backup: decrypt first, then run verify on the plain file")
		} else if op.BackupVerifySQLite(backupFile) {
			log.Infof("sqlite content ok: %s", backupFile)
		} else {
			log.Errorf("sqlite content check FAILED: %s", backupFile)
			ok = false
		}
		if !ok {
			os.Exit(1)
		}
	},
}

func init() {
	backupDecryptCmd.Flags().StringVar(&backupIn, "in", "", "encrypted backup file (.db.enc)")
	backupDecryptCmd.Flags().StringVar(&backupOut, "out", "", "output plain SQLite file (.db)")
	backupVerifyCmd.Flags().StringVar(&backupFile, "file", "", "backup file to verify")
	backupCmd.AddCommand(backupDecryptCmd, backupVerifyCmd)
	rootCmd.AddCommand(backupCmd)
}
