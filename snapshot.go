package main

import (
	"flag"
	"fmt"
	"golang.org/x/sys/unix"
	"gopkg.in/yaml.v2"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"snapshot/pushover"
	"strings"
	"time"
)

var requiredDependencies = []string{"rclone", "restic"}
var optionalDependencies = []string{"mysql", "mysqldump", "pigz"}

const (
	SuccessColor = "\033[0;32m[INFO] %s\033[0m"
	VarColor     = "\033[0;33m[INFO] %s\033[0m"
	InfoColor    = "\033[0;36m[INFO] %s\033[0m"
	ErrorColor   = "\033[1;31m[ERROR] %s\033[0m"
)

type Config struct {
	Settings struct {
		BackupDirectories []string `yaml:"backup_directories"`
		LogsPath          string   `yaml:"logs_path"`
		PigzOptions       string   `yaml:"pigz_options"`
	} `yaml:"settings"`
	Restic struct {
		Password         string `yaml:"password"`
		RepositoryPrefix string `yaml:"repository_prefix"`
		RetentionPolicy  string `yaml:"retention_policy"`
		Compression      string `yaml:"compression"`
	} `yaml:"restic"`
	Rclone struct {
		Remote string `yaml:"remote"`
	} `yaml:"rclone"`
	Database struct {
		Enabled    bool   `yaml:"enabled"`
		Repository string `yaml:"repository"`
		BackupPath string `yaml:"backup_path"`
		BackupOpts string `yaml:"backup_options"`
	} `yaml:"database"`
	Pushover struct {
		Enabled                bool   `yaml:"enabled"`
		ApiKey                 string `yaml:"api_key"`
		UserKey                string `yaml:"user_key"`
		MessageTitle           string `yaml:"message_title"`
		MessageStart           string `yaml:"message_start"`
		MessageStarted         string `yaml:"message_started"`
		MessageFailed          string `yaml:"message_failed"`
		MessageFinishedSuccess string `yaml:"message_finished_success"`
		MessageFinishedErrors  string `yaml:"message_finished_errors"`
	} `yaml:"pushover"`
}

func main() {
	var help, verbose, configSnapshots, initSnapshots, listSnapshots, pruneSnapshots, runSnapshots, unlockSnapshots bool
	var lockFilePath string = os.Args[0] + ".lock"
	lockFile, err := os.OpenFile(lockFilePath, os.O_CREATE|os.O_WRONLY, 0666)
	if err != nil {
		fmt.Printf("Error opening lock file: %v\n", err)
		return
	}
	defer lockFile.Close()

	// Try to acquire an exclusive lock
	err = acquireLock(lockFile)
	if err != nil {
		fmt.Println("Another instance is already running. Exiting.")
		return
	}
	defer releaseLock(lockFile)

	// Simulate some work being done
	/* fmt.Println("Program is running. Press Ctrl+C to stop.")
	   time.Sleep(10 * time.Second)
	*/
	var Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		flag.PrintDefaults()
	}

	// Access the configuration values
	/* fmt.Println(cfg.Settings.RepositoryPrefix)
	   fmt.Println(cfg.Settings.BackupDirectories)
	   fmt.Println(cfg.Settings.PigzOptions)
	   fmt.Println(cfg.Database.Backup)
	   fmt.Println(cfg.Database.BackupOpts)
	*/
	flag.BoolVar(&help, "help", false, "Show available options")
	flag.BoolVar(&verbose, "verbose", false, "Enable verbosity")
	flag.BoolVar(&configSnapshots, "config", false, "Configure the snapshot script")
	flag.BoolVar(&initSnapshots, "init", false, "You *MUST* run this first to initialize the repositories before snapshots can be created")
	flag.BoolVar(&listSnapshots, "list", false, "List snapshots")
	flag.BoolVar(&pruneSnapshots, "prune", false, "Prune old snapshots")
	flag.BoolVar(&runSnapshots, "run", false, "Start the snapshot")
	flag.BoolVar(&unlockSnapshots, "unlock", false, "Initial configuration")
	flag.Parse()
	var resticPath string
	for i := 0; i < len(requiredDependencies); i++ {
		path, err := exec.LookPath(requiredDependencies[i])
		if err != nil {
			fmt.Printf(ErrorColor, "Required binary ("+requiredDependencies[i]+") not found\n")
			return
		}
		if requiredDependencies[i] == "restic" {
			resticPath = path
		}
	}
	configExists, err := checkFileExistenceAndSize("config.yaml")
	if err != nil {
		log.Fatalf("Error checking configuration file: %v", err)
	}
	var cfg Config
	if configExists {
		f, err := os.Open("config.yaml")
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()

		// Create a new Config struct
		//var cfg Config

		// Decode the YAML file into the Config struct
		decoder := yaml.NewDecoder(f)
		err = decoder.Decode(&cfg)

		if err != nil {
			log.Fatal(err)
		}
	} else if len(os.Args) == 1 && !configExists {
		log.Fatalf("config.yaml does not exist. Run %s -config\n", os.Args[0])
	}
	var pushNotify = func(state string) {
		var pushoverMsg string

		// Determine the message based on the state
		switch state {
		case "started":
			pushoverMsg = cfg.Pushover.MessageStart + cfg.Pushover.MessageStarted
		case "finishedSuccess":
			pushoverMsg = cfg.Pushover.MessageStart + cfg.Pushover.MessageFinishedSuccess
		case "finishedErrors":
			pushoverMsg = cfg.Pushover.MessageStart + cfg.Pushover.MessageFinishedErrors
		case "failed":
			pushoverMsg = cfg.Pushover.MessageStart + cfg.Pushover.MessageFailed
		default:
			pushoverMsg = "Unknown state"
		}

		// Send the notification using the pushover.PushNotify method
		pushover.PushNotify(cfg.Pushover.Enabled, cfg.Pushover.ApiKey, cfg.Pushover.UserKey, cfg.Pushover.MessageTitle, pushoverMsg)
	}
	if len(os.Args) == 1 || help {
		Usage()
	} else if configSnapshots {
		if configExists {
			fmt.Printf(InfoColor, "config.yaml already exists, you are about to overwrite the configuration file!\n")
		}
		var backupDirectories string
		var logsPath string = "/var/log/snapshots"
		var compressionOpts string = "--best"
		var resticRepositoryPrefix string = "backups"
		var resticPassword string
		var resticRententionPolicy string = "--keep-within 7d"
		var resticCompression string = "max"
		var rcloneRemote string
		var databaseBackupEnabled bool = false
		var databaseRepository string = "databases"
		var databaseBackupPath string = "/backups/databases"
		var databaseBackupOpts string = "--single-transaction --skip-lock-tables"
		var pushoverEnabled bool = false
		var pushoverApiKey, pushoverUserKey string
		fmt.Println("Starting to configure snapshot YAML file")
		fmt.Print("Enter a comma-separated list (no spaces in between) of directories to backup: ")
		fmt.Scanln(&backupDirectories)
		// Split the input string into individual backup directories
		backupDirs := strings.Split(backupDirectories, ",")
		// Trim leading and trailing spaces from each directory name
		for i := range backupDirs {
			backupDirs[i] = strings.TrimSpace(backupDirs[i])
		}
		fmt.Printf("Enter path to store snapshot logs (default: %v): ", logsPath)
		fmt.Scanln(&logsPath)
		fmt.Printf("Enter compression options (default: %v): ", compressionOpts)
		fmt.Scanln(&compressionOpts)
		fmt.Print("Enter a restic repository password (do not lose this password or your snapshots will be useless): ")
		fmt.Scanln(&resticPassword)
		fmt.Printf("Enter a restic repository prefix (default: %v): ", resticRepositoryPrefix)
		fmt.Scanln(&resticRepositoryPrefix)
		fmt.Printf("Enter a restic retention policy (default: %v): ", resticRententionPolicy)
		fmt.Scanln(&resticRententionPolicy)
		fmt.Printf("Enter a restic compression (default: %v): ", resticCompression)
		fmt.Scanln(&resticCompression)
		fmt.Print("Enter a rclone remote to use to store your snapshots (e.g. rclone:someremote): ")
		fmt.Scanln(&rcloneRemote)
		fmt.Printf("Enable database backup snapshots (default: %v): ", databaseBackupEnabled)
		fmt.Scanln(&databaseBackupEnabled)
		if databaseBackupEnabled {
			fmt.Printf("Enter a database repository to store backups (default: %v): ", databaseRepository)
			fmt.Scanln(&databaseRepository)
			fmt.Printf("Enter a local backup path for temporary database backups (default: %v): ", databaseBackupPath)
			fmt.Scanln(&databaseBackupPath)
			fmt.Printf("Enter a database backup options for mysqldump (default: %v): ", databaseBackupOpts)
			fmt.Scanln(&databaseBackupOpts)
		}
		fmt.Printf("Enable pushover notifications (default: %v): ", pushoverEnabled)
		fmt.Scanln(&pushoverEnabled)
		if pushoverEnabled {
			fmt.Print("Enter a pushover API key: ")
			fmt.Scanln(&pushoverApiKey)
			fmt.Print("Enter a pushover user key: ")
			fmt.Scanln(&pushoverUserKey)
		}
		var config = Config{
			Settings: struct {
				BackupDirectories []string `yaml:"backup_directories"`
				LogsPath          string   `yaml:"logs_path"`
				PigzOptions       string   `yaml:"pigz_options"`
			}{
				BackupDirectories: backupDirs,
				LogsPath:          logsPath,
				PigzOptions:       compressionOpts,
			},
			Restic: struct {
				Password         string `yaml:"password"`
				RepositoryPrefix string `yaml:"repository_prefix"`
				RetentionPolicy  string `yaml:"retention_policy"`
				Compression      string `yaml:"compression"`
			}{
				Password:         resticPassword,
				RepositoryPrefix: resticRepositoryPrefix,
				RetentionPolicy:  resticRententionPolicy,
				Compression:      resticCompression,
			},
			Rclone: struct {
				Remote string `yaml:"remote"`
			}{
				Remote: rcloneRemote,
			},
			Database: struct {
				Enabled    bool   `yaml:"enabled"`
				Repository string `yaml:"repository"`
				BackupPath string `yaml:"backup_path"`
				BackupOpts string `yaml:"backup_options"`
			}{
				Enabled:    databaseBackupEnabled,
				Repository: databaseRepository,
				BackupPath: databaseBackupPath,
				BackupOpts: databaseBackupOpts,
			},
			Pushover: struct {
				Enabled                bool   `yaml:"enabled"`
				ApiKey                 string `yaml:"api_key"`
				UserKey                string `yaml:"user_key"`
				MessageTitle           string `yaml:"message_title"`
				MessageStart           string `yaml:"message_start"`
				MessageStarted         string `yaml:"message_started"`
				MessageFailed          string `yaml:"message_failed"`
				MessageFinishedSuccess string `yaml:"message_finished_success"`
				MessageFinishedErrors  string `yaml:"message_finished_errors"`
			}{
				Enabled:                pushoverEnabled,
				ApiKey:                 pushoverApiKey,
				UserKey:                pushoverUserKey,
				MessageTitle:           "Snapshot",
				MessageStart:           "Restic snapshot ",
				MessageStarted:         "started.",
				MessageFailed:          "failed.",
				MessageFinishedSuccess: "finished successfully.",
				MessageFinishedErrors:  "finished with errors.",
			},
		}
		err := WriteConfig("config.yaml", config)
		if err != nil {
			log.Fatalf("Error writing config to file: %v", err)
		} else {
			fmt.Println("Configuration written to config.yaml")
		}
	} else if initSnapshots {
		fmt.Println("Initializing snapshot repositories")
		if cfg.Database.Enabled {
			fmt.Println("Initializing database snapshot repository:", cfg.Database.Repository)
			command := resticPath
			args := []string{
				"-r", cfg.Rclone.Remote + ":" + cfg.Restic.RepositoryPrefix + "/" + cfg.Database.Repository,
				"init",
			}
			output, err := resticCommand(cfg.Restic.Password, cfg.Restic.Compression, command, args)
			fmt.Println(output)
			if err != nil {
				log.Fatal("Error executing restic command:", err)
			}
		}
		for i := 0; i < len(cfg.Settings.BackupDirectories); i++ {
			fmt.Println("Initializing snapshot repository:", cfg.Settings.BackupDirectories[i])
			repository := filepath.Base(cfg.Settings.BackupDirectories[i])
			command := resticPath
			args := []string{
				"-r", cfg.Rclone.Remote + ":" + cfg.Restic.RepositoryPrefix + "/" + repository,
				"init",
			}
			output, err := resticCommand(cfg.Restic.Password, cfg.Restic.Compression, command, args)
			fmt.Println(output)
			if err != nil {
				log.Fatal("Error executing restic command:", err)
			}
		}
	} else if listSnapshots {
		if cfg.Database.Enabled {
			fmt.Println("Listing database snapshots for:", cfg.Database.Repository)
			command := resticPath
			args := []string{
				"-r", cfg.Rclone.Remote + ":" + cfg.Restic.RepositoryPrefix + "/" + cfg.Database.Repository,
				"snapshots",
			}
			output, err := resticCommand(cfg.Restic.Password, cfg.Restic.Compression, command, args)
			fmt.Println(output)
			if err != nil {
				log.Fatal("Error executing restic command:", err)
			}
		}
		for i := 0; i < len(cfg.Settings.BackupDirectories); i++ {
			fmt.Println("Listing snapshots for:", cfg.Settings.BackupDirectories[i])
			repository := filepath.Base(cfg.Settings.BackupDirectories[i])
			command := resticPath
			args := []string{
				"-r", cfg.Rclone.Remote + ":" + cfg.Restic.RepositoryPrefix + "/" + repository,
				"snapshots",
			}
			output, err := resticCommand(cfg.Restic.Password, cfg.Restic.Compression, command, args)
			fmt.Println(output)
			if err != nil {
				log.Fatal("Error executing restic command:", err)
			}
		}
	} else if pruneSnapshots {
		fmt.Println("Pruning snapshots")
		if cfg.Database.Enabled {
			fmt.Println("Pruning database snapshots for:", cfg.Database.Repository)
			command := resticPath
			args := []string{
				"-r", cfg.Rclone.Remote + ":" + cfg.Restic.RepositoryPrefix + "/" + cfg.Database.Repository, "forget", "--verbose", "--prune",
			}
			retentionPolicyParts := strings.Fields(cfg.Restic.RetentionPolicy)
			for _, part := range retentionPolicyParts {
				args = append(args, part)
			}
			output, err := resticCommand(cfg.Restic.Password, cfg.Restic.Compression, command, args)
			fmt.Println(output)
			if err != nil {
				log.Fatal("Error executing restic command:", err)
			}
			fmt.Println("Checking database repository:", cfg.Restic.RepositoryPrefix+"/"+cfg.Database.Repository)
			commandCheck := resticPath
			argsCheck := []string{
				"-r", cfg.Rclone.Remote + ":" + cfg.Restic.RepositoryPrefix + "/" + cfg.Database.Repository, "check", "--verbose",
			}
			outputCheck, errCheck := resticCommand(cfg.Restic.Password, cfg.Restic.Compression, commandCheck, argsCheck)
			fmt.Println(outputCheck)
			if errCheck != nil {
				log.Fatal("Error executing restic command:", errCheck)
			}
		}
		for i := 0; i < len(cfg.Settings.BackupDirectories); i++ {
			repository := filepath.Base(cfg.Settings.BackupDirectories[i])
			fmt.Println("Pruning old snapshots from repository:", cfg.Restic.RepositoryPrefix+"/"+repository)
			command := resticPath
			args := []string{
				"-r", cfg.Rclone.Remote + ":" + cfg.Restic.RepositoryPrefix + "/" + repository, "forget", "--verbose", "--prune",
			}
			retentionPolicyParts := strings.Fields(cfg.Restic.RetentionPolicy)
			for _, part := range retentionPolicyParts {
				args = append(args, part)
			}
			output, err := resticCommand(cfg.Restic.Password, cfg.Restic.Compression, command, args)
			fmt.Println(output)
			if err != nil {
				log.Fatal("Error executing restic command:", err)
			}
			fmt.Println("Checking repository:", cfg.Restic.RepositoryPrefix+"/"+repository)
			commandCheck := resticPath
			argsCheck := []string{
				"-r", cfg.Rclone.Remote + ":" + cfg.Restic.RepositoryPrefix + "/" + repository, "check", "--verbose",
			}
			outputCheck, errCheck := resticCommand(cfg.Restic.Password, cfg.Restic.Compression, commandCheck, argsCheck)
			fmt.Println(outputCheck)
			if errCheck != nil {
				log.Fatal("Error executing restic command:", errCheck)
			}
		}
	} else if runSnapshots {
		fmt.Println("Starting snapshot")
		pushNotify("started")
		if cfg.Database.Enabled {
			var mysqlPath, mysqldumpPath, pigzPath string
			for i := 0; i < len(optionalDependencies); i++ {
				path, err := exec.LookPath(optionalDependencies[i])
				if err != nil {
					if cfg.Database.Enabled {
						fmt.Printf(ErrorColor, "Missing binary ("+optionalDependencies[i]+") that is required for database backups to be performed.\n")
						return
					}
				}
				if optionalDependencies[i] == "mysql" {
					mysqlPath = path
				}
				if optionalDependencies[i] == "mysqldump" {
					mysqldumpPath = path
				}
				if optionalDependencies[i] == "pigz" {
					pigzPath = path
				}
			}
			// Create the directory, including any necessary parent directories
			err := os.MkdirAll(cfg.Database.BackupPath, 0700)
			if err != nil {
				// If there is an error, log it and return
				fmt.Println("Error creating database backup directory:", err)
				return
			}
			fmt.Println("Generating a list of databases to backup.")
			commandGetDbs := mysqlPath
			argsGetDbs := []string{
				"-BNe", "SHOW DATABASES",
			}
			outputGetDbs, errGetDbs := runCommand(commandGetDbs, argsGetDbs)
			if errGetDbs != nil {
				log.Fatal("Error getting databases:", errGetDbs)
			}

			databases := strings.Split(strings.TrimSpace(string(outputGetDbs)), "\n")

			var databasesBackup []string

			for _, database := range databases {
				if database == "information_schema" || database == "performance_schema" {
					continue // Skip system databases
				}
				dbFilename := fmt.Sprintf("%s-%s.sql.gz", database, time.Now().Format("2006-01-02T15:04"))
				cmd := exec.Command(mysqldumpPath, cfg.Database.BackupOpts, database)
				pigzCmd := exec.Command(pigzPath, cfg.Database.BackupOpts)

				pigzCmd.Stdin, _ = cmd.StdoutPipe()
				pigzCmd.Stdout, _ = os.Create(fmt.Sprintf("%s/%s", cfg.Database.BackupPath, dbFilename))

				if err := cmd.Start(); err != nil {
					log.Fatal("Error starting mysqldump:", err)
				}

				if err := pigzCmd.Start(); err != nil {
					log.Fatal("Error starting pigz:", err)
				}

				// Wait for commands to finish
				if err := cmd.Wait(); err != nil {
					log.Fatal("Error with mysqldump:", err)
				}
				if err := pigzCmd.Wait(); err != nil {
					log.Fatal("Error with pigz:", err)
				}

				// Add the filename to the list
				databasesBackup = append(databasesBackup, dbFilename)
			}
			command := resticPath
			args := []string{
				"-r", cfg.Rclone.Remote + ":" + cfg.Restic.RepositoryPrefix + "/" + cfg.Database.Repository, "backup", "--verbose", "--exclude-if-present", ".resticignore", cfg.Database.BackupPath,
			}
			output, err := resticCommand(cfg.Restic.Password, cfg.Restic.Compression, command, args)
			fmt.Println(output)
			if err != nil {
				for _, dbBackup := range databasesBackup {
					err := os.Remove(fmt.Sprintf("%s/%s", cfg.Database.BackupPath, dbBackup))
					if err != nil {
						log.Printf("Failed to remove backup file: %s\n", dbBackup)
					}
				}
			}
		}
		for i := 0; i < len(cfg.Settings.BackupDirectories); i++ {
			fmt.Println("Backing up directory:", cfg.Settings.BackupDirectories[i])
			repository := filepath.Base(cfg.Settings.BackupDirectories[i])
			command := resticPath
			args := []string{
				"-r", cfg.Rclone.Remote + ":" + cfg.Restic.RepositoryPrefix + "/" + repository, "backup", "--verbose", "--exclude-if-present", ".resticignore", cfg.Settings.BackupDirectories[i],
			}
			output, err := resticCommand(cfg.Restic.Password, cfg.Restic.Compression, command, args)
			fmt.Println(output)
			if err != nil {
				log.Fatal("Error executing restic command:", err)
			}
			pushNotify("finishedSuccess")
		}
	} else if unlockSnapshots {
		fmt.Println("Unlocking repositories")
		if cfg.Database.Enabled {
			fmt.Println("Unlocking database repository for:", cfg.Database.Repository)
			command := resticPath
			args := []string{
				"-r", cfg.Rclone.Remote + ":" + cfg.Restic.RepositoryPrefix + "/" + cfg.Database.Repository, "unlock", "--verbose",
			}
			output, err := resticCommand(cfg.Restic.Password, cfg.Restic.Compression, command, args)
			fmt.Println(output)
			if err != nil {
				log.Fatal("Error executing restic command:", err)
			}
			fmt.Println("Checking database repository:", cfg.Restic.RepositoryPrefix+"/"+cfg.Database.Repository)
			commandCheck := resticPath
			argsCheck := []string{
				"-r", cfg.Rclone.Remote + ":" + cfg.Restic.RepositoryPrefix + "/" + cfg.Database.Repository, "check", "--verbose",
			}
			outputCheck, errCheck := resticCommand(cfg.Restic.Password, cfg.Restic.Compression, commandCheck, argsCheck)
			fmt.Println(outputCheck)
			if errCheck != nil {
				log.Fatal("Error executing restic command:", errCheck)
			}
		}
		for i := 0; i < len(cfg.Settings.BackupDirectories); i++ {
			repository := filepath.Base(cfg.Settings.BackupDirectories[i])
			fmt.Println("Unlocking repository:", cfg.Restic.RepositoryPrefix+"/"+repository)
			command := resticPath
			args := []string{
				"-r", cfg.Rclone.Remote + ":" + cfg.Restic.RepositoryPrefix + "/" + repository, "unlock", "--verbose",
			}
			output, err := resticCommand(cfg.Restic.Password, cfg.Restic.Compression, command, args)
			fmt.Println(output)
			if err != nil {
				log.Fatal("Error executing restic command:", err)
			}
			fmt.Println("Checking repository:", cfg.Restic.RepositoryPrefix+"/"+repository)
			commandCheck := resticPath
			argsCheck := []string{
				"-r", cfg.Rclone.Remote + ":" + cfg.Restic.RepositoryPrefix + "/" + repository, "check", "--verbose",
			}
			outputCheck, errCheck := resticCommand(cfg.Restic.Password, cfg.Restic.Compression, commandCheck, argsCheck)
			fmt.Println(outputCheck)
			if errCheck != nil {
				log.Fatal("Error executing restic command:", errCheck)
			}
		}
	} else {
		Usage()
	}
}
func WriteConfig(filename string, config Config) error {
	// Marshal the config struct to YAML
	data, err := yaml.Marshal(&config)
	if err != nil {
		return fmt.Errorf("error marshaling config to YAML: %v", err)
	}

	// Create or overwrite the file (using os.Create)
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("error creating file: %v", err)
	}
	defer file.Close()

	// Write the YAML data to the file
	_, err = file.Write(data)
	if err != nil {
		return fmt.Errorf("error writing to file: %v", err)
	}

	return nil
}
func acquireLock(lockFile *os.File) error {
	// Try to acquire an exclusive lock (flock)
	// This will fail if the file is already locked by another process
	err := unix.Flock(int(lockFile.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err != nil {
		// If the error is non-nil, then we cannot acquire the lock
		return fmt.Errorf("failed to acquire lock: %v", err)
	}
	return nil
}

func releaseLock(lockFile *os.File) {
	// Release the lock when we're done
	unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)
	//fmt.Println("Lock released.")
}
func checkFileExistenceAndSize(filename string) (bool, error) {
	// Get file information using os.Stat
	fileInfo, err := os.Stat(filename)
	if err != nil {
		// If the error is not nil, it means the file doesn't exist
		if os.IsNotExist(err) {
			return false, nil // File does not exist
		}
		// Some other error occurred
		return false, err
	}

	// Check if the file is greater than 0 bytes
	if fileInfo.Size() > 0 {
		return true, nil // File exists and has a size greater than 0 bytes
	}

	return false, nil // File exists but has 0 bytes
}
func setEnvVars(vars map[string]string) {
	for key, value := range vars {
		err := os.Setenv(key, value)
		if err != nil {
			log.Fatalf("Failed to set environment variable %s: %v", key, err)
		}
	}
}
func resticCommand(resticPassword, resticCompression, resticCmd string, args []string) (string, error) {
	envVars := map[string]string{
		"RESTIC_PASSWORD":    resticPassword,
		"RESTIC_COMPRESSION": resticCompression,
	}

	// Set the environment variables using the helper function
	setEnvVars(envVars)

	// Prepare the restic command
	cmd := exec.Command(resticCmd, args...)

	// Run the command and capture the output
	output, err := cmd.CombinedOutput()
	return string(output), err
}
func runCommand(command string, args []string) (string, error) {
	cmd := exec.Command(command, args...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}
