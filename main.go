package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/fsnotify/fsnotify"
)

// Version can be set at build time
var Version = "dev"

// S3Uploader defines the interface for S3 operations.
// Using an interface allows us to mock S3 interactions for easy testing.
type S3Uploader interface {
	Upload(ctx context.Context, input *s3.PutObjectInput) (*s3.PutObjectOutput, error)
	DeleteObject(ctx context.Context, input *s3.DeleteObjectInput) (*s3.DeleteObjectOutput, error)
}

// S3Client is a wrapper for the official AWS S3 client that implements our S3Uploader interface.
type S3Client struct {
	client *s3.Client
}

// Upload uploads a file to an S3 bucket.
func (c *S3Client) Upload(ctx context.Context, input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
	return c.client.PutObject(ctx, input)
}

// DeleteObject deletes an object from an S3 bucket.
func (c *S3Client) DeleteObject(ctx context.Context, input *s3.DeleteObjectInput) (*s3.DeleteObjectOutput, error) {
	return c.client.DeleteObject(ctx, input)
}

// S3ClientCreator is a function type for creating S3 clients
type S3ClientCreator func(ctx context.Context) (*S3Client, error)

// newS3Client creates a new S3 client wrapper.
var newS3Client S3ClientCreator = func(ctx context.Context) (*S3Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to load AWS SDK config: %w", err)
	}
	return &S3Client{client: s3.NewFromConfig(cfg)}, nil
}

// App holds the application's configuration and dependencies.
type App struct {
	uploader     S3Uploader
	localPath    string
	isDir        bool // True if localPath is a directory
	bucket       string
	keyPrefix    string
	delete       bool
	storageClass types.StorageClass
}

// AppConfig holds the configuration for the application.
type AppConfig struct {
	LocalPath    string
	Bucket       string
	KeyPrefix    string
	Delete       bool
	StorageClass types.StorageClass
}

// parseFlags parses command-line flags and returns the configuration.
func parseFlags() (showVersion bool, config *AppConfig, args []string, err error) {
	deleteFlag := flag.Bool("delete", false, "Delete files in S3 when they are deleted locally.")
	storageClassFlag := flag.String("storage-class", string(types.StorageClassIntelligentTiering), "Specify the S3 storage class (e.g., STANDARD, GLACIER).")
	versionFlag := flag.Bool("version", false, "Print the echos3 version and exit.")
	flag.Parse()

	config = &AppConfig{
		Delete:       *deleteFlag,
		StorageClass: types.StorageClass(*storageClassFlag),
	}

	return *versionFlag, config, flag.Args(), nil
}

// validateArgs validates command-line arguments and returns the local path and S3 path.
func validateArgs(args []string) (string, string, error) {
	if len(args) != 2 {
		return "", "", errors.New("incorrect number of arguments")
	}
	return args[0], args[1], nil
}

// setupLocalPath validates and sets up the local path.
func setupLocalPath(path string) (string, os.FileInfo, error) {
	localPath, err := filepath.Abs(path)
	if err != nil {
		return "", nil, fmt.Errorf("invalid local path: %w", err)
	}

	pathInfo, err := os.Stat(localPath)
	if err != nil {
		return "", nil, fmt.Errorf("could not access path %s: %w", localPath, err)
	}

	return localPath, pathInfo, nil
}

// createApp creates a new App instance with the given configuration.
func createApp(ctx context.Context, config *AppConfig, localPath string, isDir bool) (*App, error) {
	s3Client, err := newS3Client(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create S3 client: %w", err)
	}

	return &App{
		uploader:     s3Client,
		localPath:    localPath,
		isDir:        isDir,
		bucket:       config.Bucket,
		keyPrefix:    config.KeyPrefix,
		delete:       config.Delete,
		storageClass: config.StorageClass,
	}, nil
}

// main is the entry point of the application.
func main() {
	// Parse flags
	showVersion, config, args, err := parseFlags()
	if err != nil {
		log.Fatalf("FATAL: Failed to parse flags: %v", err)
	}

	if showVersion {
		fmt.Printf("echos3 version %s\n", Version)
		os.Exit(0)
	}

	// Validate arguments
	localPathArg, s3Path, err := validateArgs(args)
	if err != nil {
		log.Fatal("Usage: echos3 /path/to/watch s3://bucket/key [--delete] [--storage-class STORAGE_CLASS]")
	}

	// Setup local path
	localPath, pathInfo, err := setupLocalPath(localPathArg)
	if err != nil {
		log.Fatalf("FATAL: %v", err)
	}

	// Parse S3 path
	bucket, keyPrefix, err := parseS3Path(s3Path)
	if err != nil {
		log.Fatalf("FATAL: Invalid S3 path: %v", err)
	}
	config.Bucket = bucket
	config.KeyPrefix = keyPrefix
	config.LocalPath = localPath

	// Create and run the application
	ctx := context.Background()
	app, err := createApp(ctx, config, localPath, pathInfo.IsDir())
	if err != nil {
		log.Fatalf("FATAL: %v", err)
	}

	if err := app.run(ctx); err != nil {
		log.Fatalf("FATAL: Application failed: %v", err)
	}
}

// run starts the file watcher and handles events.
func (a *App) run(ctx context.Context) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("could not create file watcher: %w", err)
	}
	defer func() {
		if err := watcher.Close(); err != nil {
			log.Printf("ERROR: Could not close watcher: %v", err)
		}
	}()

	if a.isDir {
		// If the path is a directory, walk it and add all subdirectories.
		log.Printf("INFO: Performing initial scan of directory %s...", a.localPath)
		err = filepath.Walk(a.localPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				if err := watcher.Add(path); err != nil {
					return fmt.Errorf("failed to add path to watcher %s: %w", path, err)
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("error during initial directory scan: %w", err)
		}
		log.Printf("INFO: Watching directory for changes. Uploading to s3://%s/%s", a.bucket, a.keyPrefix)
	} else {
		// If the path is a file, watch its parent directory.
		parentDir := filepath.Dir(a.localPath)
		log.Printf("INFO: Watching single file %s in directory %s", filepath.Base(a.localPath), parentDir)
		if err := watcher.Add(parentDir); err != nil {
			return fmt.Errorf("failed to watch directory %s for file changes: %w", parentDir, err)
		}
	}

	// Main event loop
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			a.handleEvent(ctx, event, watcher)
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			log.Printf("ERROR: Watcher error: %v", err)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// handleEvent processes a single file system event.
func (a *App) handleEvent(ctx context.Context, event fsnotify.Event, watcher *fsnotify.Watcher) {
	// If watching a single file, ignore events for any other file.
	if !a.isDir && event.Name != a.localPath {
		return
	}

	var s3Key string
	if a.isDir {
		// For directories, the S3 key is relative to the watched directory.
		relPath, err := filepath.Rel(a.localPath, event.Name)
		if err != nil {
			log.Printf("ERROR: Could not determine relative path for %s: %v", event.Name, err)
			return
		}
		s3Key = filepath.ToSlash(filepath.Join(a.keyPrefix, relPath))
	} else {
		// For a single file, the S3 key is simply the key prefix provided.
		s3Key = a.keyPrefix
	}

	op := event.Op
	// Handle writes, creates, and renames as upload events
	if op&fsnotify.Write == fsnotify.Write || op&fsnotify.Create == fsnotify.Create || op&fsnotify.Rename == fsnotify.Rename {
		// Wait a moment to handle rapid writes (e.g., from editors saving)
		time.Sleep(100 * time.Millisecond)
		info, err := os.Stat(event.Name)
		if err != nil {
			if os.IsNotExist(err) {
				a.handleRemove(ctx, s3Key)
			} else {
				log.Printf("ERROR: Could not stat file %s: %v", event.Name, err)
			}
			return
		}

		if info.IsDir() {
			if a.isDir { // Only add new directories if we are watching a directory tree
				if err := watcher.Add(event.Name); err != nil {
					log.Printf("ERROR: Failed to add new directory to watcher %s: %v", event.Name, err)
				} else {
					log.Printf("INFO: Watching new directory: %s", event.Name)
				}
			}
		} else {
			a.handleUpload(ctx, event.Name, s3Key)
		}
	} else if op&fsnotify.Remove == fsnotify.Remove {
		a.handleRemove(ctx, s3Key)
	}
}

// handleUpload uploads a single file to S3.
func (a *App) handleUpload(ctx context.Context, localFile, s3Key string) {
	file, err := os.Open(localFile)
	if err != nil {
		log.Printf("ERROR: Could not open file for upload %s: %v", localFile, err)
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Printf("ERROR: Could not close file %s: %v", localFile, err)
		}
	}()

	s3URI := fmt.Sprintf("s3://%s/%s", a.bucket, s3Key)
	log.Printf("UPLOAD: %s -> %s", filepath.Base(localFile), s3URI)

	input := &s3.PutObjectInput{
		Bucket:       aws.String(a.bucket),
		Key:          aws.String(s3Key),
		Body:         file,
		StorageClass: a.storageClass,
	}

	_, err = a.uploader.Upload(ctx, input)
	if err != nil {
		log.Printf("ERROR: Failed to upload %s: %v", localFile, err)
	}
}

// handleRemove deletes a single object from S3 if the --delete flag is set.
func (a *App) handleRemove(ctx context.Context, s3Key string) {
	if !a.delete {
		log.Printf("INFO: File removed locally but --delete is not set. Ignoring: %s", s3Key)
		return
	}

	s3URI := fmt.Sprintf("s3://%s/%s", a.bucket, s3Key)
	log.Printf("DELETE: %s", s3URI)
	input := &s3.DeleteObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(s3Key),
	}
	_, err := a.uploader.DeleteObject(ctx, input)
	if err != nil {
		log.Printf("ERROR: Failed to delete %s from S3: %v", s3Key, err)
	}
}

// parseS3Path parses an S3 path string (e.g., "s3://bucket/key/prefix")
// into a bucket and a key prefix.
func parseS3Path(s3Path string) (bucket, keyPrefix string, err error) {
	if !strings.HasPrefix(s3Path, "s3://") {
		return "", "", errors.New("S3 path must start with s3://")
	}
	trimmed := strings.TrimPrefix(s3Path, "s3://")
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		return "", "", errors.New("invalid S3 path format: missing bucket name")
	}

	bucket = parts[0]
	if len(parts) > 1 {
		keyPrefix = parts[1]
	}

	return bucket, keyPrefix, nil
}
