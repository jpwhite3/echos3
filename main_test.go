package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testBinaryPath holds the path to the compiled binary for integration tests.
var testBinaryPath string

// TestMain compiles the application binary once before running tests.
// This is used for integration tests that execute the CLI directly.
func TestMain(m *testing.M) {
	var err error
	// Create a temporary directory for the compiled binary
	tmpDir, err := os.MkdirTemp("", "test-bin")
	if err != nil {
		log.Fatalf("failed to create temp dir for test binary: %v", err)
	}
	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			log.Printf("Error removing temp dir %s: %v", tmpDir, err)
		}
	}()

	testBinaryPath = filepath.Join(tmpDir, "echos3")
	if runtime.GOOS == "windows" {
		testBinaryPath += ".exe"
	}

	// Build the binary with a specific version for testing
	buildCmd := exec.Command("go", "build", "-ldflags", "-X main.Version=test", "-o", testBinaryPath, ".")
	if output, err := buildCmd.CombinedOutput(); err != nil {
		log.Fatalf("failed to build test binary: %s", output)
	}

	// Run all tests
	code := m.Run()
	os.Exit(code)
}

// MockS3Uploader is a mock implementation of the S3Uploader interface for testing.
type MockS3Uploader struct {
	Uploads   map[string]*s3.PutObjectInput
	Deletes   map[string]*s3.DeleteObjectInput
	UploadErr error
	DeleteErr error
}

func newMockS3Uploader() *MockS3Uploader {
	return &MockS3Uploader{
		Uploads: make(map[string]*s3.PutObjectInput),
		Deletes: make(map[string]*s3.DeleteObjectInput),
	}
}

func (m *MockS3Uploader) Upload(_ context.Context, input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
	if m.UploadErr != nil {
		return nil, m.UploadErr
	}
	m.Uploads[*input.Key] = input
	return &s3.PutObjectOutput{}, nil
}

func (m *MockS3Uploader) DeleteObject(_ context.Context, input *s3.DeleteObjectInput) (*s3.DeleteObjectOutput, error) {
	if m.DeleteErr != nil {
		return nil, m.DeleteErr
	}
	m.Deletes[*input.Key] = input
	return &s3.DeleteObjectOutput{}, nil
}

// newTestApp is a helper to set up the App struct for testing.
func newTestApp(t *testing.T, deleteFlag bool, isDir bool) (*App, *MockS3Uploader, string) {
	t.Helper()
	tmpDir := t.TempDir()
	mockUploader := newMockS3Uploader()

	app := &App{
		uploader:     mockUploader,
		localPath:    tmpDir, // Default to dir, can be overridden by caller
		isDir:        isDir,
		bucket:       "test-bucket",
		keyPrefix:    "test-prefix",
		delete:       deleteFlag,
		storageClass: types.StorageClassStandard,
	}
	return app, mockUploader, tmpDir
}

func TestParseS3Path(t *testing.T) {
	testCases := []struct {
		name         string
		path         string
		expectBucket string
		expectKey    string
		expectErr    bool
	}{
		{"Valid path with key", "s3://my-bucket/path/to/key", "my-bucket", "path/to/key", false},
		{"Valid path with trailing slash", "s3://my-bucket/path/", "my-bucket", "path/", false},
		{"Valid path with no key", "s3://my-bucket", "my-bucket", "", false},
		{"Valid path bucket only", "s3://my-bucket/", "my-bucket", "", false},
		{"Path with double slashes", "s3://my-bucket//path/key", "my-bucket", "/path/key", false},
		{"Invalid scheme", "http://my-bucket/path", "", "", true},
		{"No scheme", "my-bucket/path", "", "", true},
		{"No bucket", "s3://", "", "", true},
		{"No bucket with slash", "s3:///", "", "", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			bucket, key, err := parseS3Path(tc.path)
			assert.Equal(t, tc.expectBucket, bucket)
			assert.Equal(t, tc.expectKey, key)
			if tc.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestApp_handleEvent(t *testing.T) {
	watcher, err := fsnotify.NewWatcher()
	require.NoError(t, err)
	defer func() {
		if err := watcher.Close(); err != nil {
			t.Logf("Error closing watcher: %v", err)
		}
	}()

	t.Run("Directory Watch", func(t *testing.T) {
		t.Run("Create file should trigger upload with relative key", func(t *testing.T) {
			app, mockUploader, tmpDir := newTestApp(t, false, true) // isDir = true
			testFile := filepath.Join(tmpDir, "newfile.txt")
			require.NoError(t, os.WriteFile(testFile, []byte("content"), 0644))

			event := fsnotify.Event{Name: testFile, Op: fsnotify.Create}
			app.handleEvent(context.Background(), event, watcher)

			expectedKey := "test-prefix/newfile.txt"
			assert.Contains(t, mockUploader.Uploads, expectedKey)
		})

		t.Run("Remove file should trigger delete if flag is set", func(t *testing.T) {
			app, mockUploader, tmpDir := newTestApp(t, true, true) // delete = true, isDir = true
			testFile := filepath.Join(tmpDir, "delete.txt")

			event := fsnotify.Event{Name: testFile, Op: fsnotify.Remove}
			app.handleEvent(context.Background(), event, watcher)

			expectedKey := "test-prefix/delete.txt"
			assert.Contains(t, mockUploader.Deletes, expectedKey)
			assert.Empty(t, mockUploader.Uploads)
		})
	})

	t.Run("Single File Watch", func(t *testing.T) {
		t.Run("Event on watched file should trigger upload with fixed key", func(t *testing.T) {
			app, mockUploader, tmpDir := newTestApp(t, false, false) // isDir = false
			watchedFile := filepath.Join(tmpDir, "watched.txt")
			app.localPath = watchedFile // Explicitly set the path to the file
			require.NoError(t, os.WriteFile(watchedFile, []byte("content"), 0644))

			event := fsnotify.Event{Name: watchedFile, Op: fsnotify.Write}
			app.handleEvent(context.Background(), event, watcher)

			expectedKey := "test-prefix" // For single file, key is the prefix
			assert.Contains(t, mockUploader.Uploads, expectedKey)
		})

		t.Run("Event on other file should be ignored", func(t *testing.T) {
			app, mockUploader, tmpDir := newTestApp(t, false, false) // isDir = false
			watchedFile := filepath.Join(tmpDir, "watched.txt")
			otherFile := filepath.Join(tmpDir, "other.txt")
			app.localPath = watchedFile
			require.NoError(t, os.WriteFile(otherFile, []byte("content"), 0644))

			event := fsnotify.Event{Name: otherFile, Op: fsnotify.Write}
			app.handleEvent(context.Background(), event, watcher)

			assert.Empty(t, mockUploader.Uploads, "Should not upload for an unwatched file")
		})

		t.Run("Remove watched file should trigger delete if flag is set", func(t *testing.T) {
			app, mockUploader, tmpDir := newTestApp(t, true, false) // delete = true, isDir = false
			watchedFile := filepath.Join(tmpDir, "watched.txt")
			app.localPath = watchedFile

			event := fsnotify.Event{Name: watchedFile, Op: fsnotify.Remove}
			app.handleEvent(context.Background(), event, watcher)

			expectedKey := "test-prefix"
			assert.Contains(t, mockUploader.Deletes, expectedKey)
		})
	})
}

func TestApp_handleUpload_Errors(t *testing.T) {
	t.Run("Fails when file cannot be opened", func(t *testing.T) {
		app, mockUploader, tmpDir := newTestApp(t, false, true)
		nonExistentFile := filepath.Join(tmpDir, "ghost.txt")

		app.handleUpload(context.Background(), nonExistentFile, "test-prefix/ghost.txt")

		assert.Empty(t, mockUploader.Uploads, "Upload should not be attempted if file doesn't exist")
	})

	t.Run("Fails when S3 upload returns an error", func(t *testing.T) {
		app, mockUploader, tmpDir := newTestApp(t, false, true)
		mockUploader.UploadErr = errors.New("S3 is down")
		testFile := filepath.Join(tmpDir, "upload-fail.txt")
		require.NoError(t, os.WriteFile(testFile, []byte("content"), 0644))

		app.handleUpload(context.Background(), testFile, "test-prefix/upload-fail.txt")

		assert.Empty(t, mockUploader.Uploads)
	})
}

func TestApp_handleRemove_Errors(t *testing.T) {
	t.Run("Fails when S3 delete returns an error", func(t *testing.T) {
		app, mockUploader, _ := newTestApp(t, true, true)
		mockUploader.DeleteErr = errors.New("S3 is down")

		app.handleRemove(context.Background(), "test-prefix/delete-fail.txt")
		assert.Empty(t, mockUploader.Deletes)
	})
}

func TestApp_run_Errors(t *testing.T) {
	t.Run("Fails when initial directory scan fails", func(t *testing.T) {
		app, _, _ := newTestApp(t, false, true) // isDir = true
		app.localPath = "/path/that/does/not/exist"

		err := app.run(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "error during initial directory scan")
	})

	t.Run("Fails to watch parent dir for single file", func(t *testing.T) {
		app, _, _ := newTestApp(t, false, false) // isDir = false
		// This is hard to test deterministically without a mock fsnotify,
		// but we can test the error path if the parent directory doesn't exist.
		app.localPath = "/non-existent-dir/some-file.txt"
		err := app.run(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to watch directory")
	})
}

// ---- Integration Tests ----

func TestIntegration_VersionFlag(t *testing.T) {
	cmd := exec.Command(testBinaryPath, "--version")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Command should exit cleanly")
	assert.Contains(t, string(output), "echos3 version test", "Version flag should print the correct version")
}

func TestIntegration_ArgumentValidation(t *testing.T) {
	testCases := []struct {
		name string
		args []string
	}{
		{"No arguments", []string{}},
		{"One argument", []string{"./tmp"}},
		{"Too many arguments", []string{"./tmp", "s3://bucket", "extra"}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(testBinaryPath, tc.args...)
			output, err := cmd.CombinedOutput()
			require.Error(t, err, "Command should fail with wrong number of arguments")
			assert.Contains(t, string(output), "Usage: echos3", "Should print usage information on error")
		})
	}
}

func TestIntegration_ValidArguments(t *testing.T) {
	// Skip this test in short mode as it involves file system operations
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Create a temporary directory for the test
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("test content"), 0644))

	// Create a mock S3 bucket name (we won't actually connect to S3)
	bucketName := "test-bucket-" + filepath.Base(tmpDir)
	s3Path := "s3://" + bucketName + "/test-prefix"

	// Run the command with the --version flag to avoid actually starting the watcher
	// We just want to verify that argument parsing works correctly
	cmd := exec.Command(testBinaryPath, "--version", testFile, s3Path)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Command should exit cleanly with version flag")
	assert.Contains(t, string(output), "echos3 version test", "Version flag should print the correct version")

	// Test with storage class flag
	cmd = exec.Command(testBinaryPath, "--version", "--storage-class", "STANDARD", testFile, s3Path)
	output, err = cmd.CombinedOutput()
	require.NoError(t, err, "Command should exit cleanly with version and storage class flags")
	assert.Contains(t, string(output), "echos3 version test", "Version flag should print the correct version")

	// Test with delete flag
	cmd = exec.Command(testBinaryPath, "--version", "--delete", testFile, s3Path)
	output, err = cmd.CombinedOutput()
	require.NoError(t, err, "Command should exit cleanly with version and delete flags")
	assert.Contains(t, string(output), "echos3 version test", "Version flag should print the correct version")
}

func TestParseFlags(t *testing.T) {
	// Save original command line arguments and restore them after the test
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	testCases := []struct {
		name           string
		args           []string
		expectVersion  bool
		expectDelete   bool
		expectStorageClass string
	}{
		{
			name:           "Default flags",
			args:           []string{"echos3", "local/path", "s3://bucket/key"},
			expectVersion:  false,
			expectDelete:   false,
			expectStorageClass: string(types.StorageClassIntelligentTiering),
		},
		{
			name:           "Version flag",
			args:           []string{"echos3", "--version"},
			expectVersion:  true,
			expectDelete:   false,
			expectStorageClass: string(types.StorageClassIntelligentTiering),
		},
		{
			name:           "Delete flag",
			args:           []string{"echos3", "--delete", "local/path", "s3://bucket/key"},
			expectVersion:  false,
			expectDelete:   true,
			expectStorageClass: string(types.StorageClassIntelligentTiering),
		},
		{
			name:           "Storage class flag",
			args:           []string{"echos3", "--storage-class", "GLACIER", "local/path", "s3://bucket/key"},
			expectVersion:  false,
			expectDelete:   false,
			expectStorageClass: "GLACIER",
		},
		{
			name:           "All flags",
			args:           []string{"echos3", "--version", "--delete", "--storage-class", "STANDARD", "local/path", "s3://bucket/key"},
			expectVersion:  true,
			expectDelete:   true,
			expectStorageClass: "STANDARD",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset flags for each test case
			flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
			
			// Set up test arguments
			os.Args = tc.args
			
			// Call the function
			showVersion, config, args, err := parseFlags()
			
			// Check results
			assert.NoError(t, err)
			assert.Equal(t, tc.expectVersion, showVersion)
			assert.Equal(t, tc.expectDelete, config.Delete)
			assert.Equal(t, types.StorageClass(tc.expectStorageClass), config.StorageClass)
			
			// Check that args contains the non-flag arguments
			expectedArgs := []string{}
			for _, arg := range tc.args[1:] {
				if !strings.HasPrefix(arg, "--") &&
				   arg != "GLACIER" && arg != "STANDARD" { // Skip flag values
					expectedArgs = append(expectedArgs, arg)
				}
			}
			assert.Equal(t, expectedArgs, args)
		})
	}
}

func TestValidateArgs(t *testing.T) {
	testCases := []struct {
		name        string
		args        []string
		expectLocal string
		expectS3    string
		expectErr   bool
	}{
		{
			name:        "Valid arguments",
			args:        []string{"/local/path", "s3://bucket/key"},
			expectLocal: "/local/path",
			expectS3:    "s3://bucket/key",
			expectErr:   false,
		},
		{
			name:        "No arguments",
			args:        []string{},
			expectLocal: "",
			expectS3:    "",
			expectErr:   true,
		},
		{
			name:        "One argument",
			args:        []string{"/local/path"},
			expectLocal: "",
			expectS3:    "",
			expectErr:   true,
		},
		{
			name:        "Too many arguments",
			args:        []string{"/local/path", "s3://bucket/key", "extra"},
			expectLocal: "",
			expectS3:    "",
			expectErr:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			localPath, s3Path, err := validateArgs(tc.args)
			
			if tc.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectLocal, localPath)
				assert.Equal(t, tc.expectS3, s3Path)
			}
		})
	}
}

func TestSetupLocalPath(t *testing.T) {
	t.Run("Valid path", func(t *testing.T) {
		// Create a temporary directory for testing
		tmpDir := t.TempDir()
		
		// Call the function
		path, info, err := setupLocalPath(tmpDir)
		
		// Check results
		assert.NoError(t, err)
		assert.True(t, info.IsDir())
		
		// The path should be absolute
		absPath, _ := filepath.Abs(tmpDir)
		assert.Equal(t, absPath, path)
	})
	
	t.Run("Valid file", func(t *testing.T) {
		// Create a temporary file for testing
		tmpFile, err := os.CreateTemp("", "test-file")
		require.NoError(t, err)
		defer os.Remove(tmpFile.Name())
		
		// Call the function
		path, info, err := setupLocalPath(tmpFile.Name())
		
		// Check results
		assert.NoError(t, err)
		assert.False(t, info.IsDir())
		
		// The path should be absolute
		absPath, _ := filepath.Abs(tmpFile.Name())
		assert.Equal(t, absPath, path)
	})
	
	t.Run("Non-existent path", func(t *testing.T) {
		// Call the function with a non-existent path
		_, _, err := setupLocalPath("/path/that/does/not/exist")
		
		// Check results
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "could not access path")
	})
}

func TestCreateApp(t *testing.T) {
	// Save the original S3 client creator and restore it after the test
	originalNewS3Client := newS3Client
	defer func() {
		newS3Client = originalNewS3Client
	}()
	
	// Set up a mock S3 client creator that returns a valid client
	newS3Client = func(ctx context.Context) (*S3Client, error) {
		return &S3Client{client: nil}, nil
	}
	
	config := &AppConfig{
		LocalPath:    "/test/path",
		Bucket:       "test-bucket",
		KeyPrefix:    "test-prefix",
		Delete:       true,
		StorageClass: types.StorageClassStandard,
	}
	
	t.Run("Create app with directory", func(t *testing.T) {
		app, err := createApp(context.Background(), config, "/test/path", true)
		
		assert.NoError(t, err)
		assert.NotNil(t, app)
		assert.Equal(t, "/test/path", app.localPath)
		assert.True(t, app.isDir)
		assert.Equal(t, "test-bucket", app.bucket)
		assert.Equal(t, "test-prefix", app.keyPrefix)
		assert.True(t, app.delete)
		assert.Equal(t, types.StorageClassStandard, app.storageClass)
	})
	
	t.Run("Create app with file", func(t *testing.T) {
		app, err := createApp(context.Background(), config, "/test/path/file.txt", false)
		
		assert.NoError(t, err)
		assert.NotNil(t, app)
		assert.Equal(t, "/test/path/file.txt", app.localPath)
		assert.False(t, app.isDir)
	})
	
	t.Run("S3 client creation failure", func(t *testing.T) {
		// Make newS3Client return an error
		newS3Client = func(ctx context.Context) (*S3Client, error) {
			return nil, errors.New("failed to create S3 client")
		}
		
		_, err := createApp(context.Background(), config, "/test/path", true)
		
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create S3 client")
	})
}

func TestMainFlow(t *testing.T) {
	// This test simulates the flow of the main function by calling the extracted functions
	// in sequence, allowing us to test the main function's logic without directly testing main()
	
	// Save the original S3 client creator and restore it after the test
	originalNewS3Client := newS3Client
	defer func() { 
		newS3Client = originalNewS3Client 
	}()
	
	// Create a mock S3 client creator
	newS3Client = func(ctx context.Context) (*S3Client, error) {
		return &S3Client{client: nil}, nil
	}
	
	// Create a temporary directory and file for testing
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("test content"), 0644))
	
	// Save original command line arguments and restore them after the test
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	
	// Set up test arguments
	os.Args = []string{"echos3", "--storage-class", "STANDARD", testFile, "s3://test-bucket/test-prefix"}
	
	// Reset flags for the test
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	
	// Step 1: Parse flags
	showVersion, config, args, err := parseFlags()
	require.NoError(t, err)
	assert.False(t, showVersion)
	assert.Equal(t, types.StorageClassStandard, config.StorageClass)
	
	// Step 2: Validate arguments
	localPathArg, s3Path, err := validateArgs(args)
	require.NoError(t, err)
	assert.Equal(t, testFile, localPathArg)
	assert.Equal(t, "s3://test-bucket/test-prefix", s3Path)
	
	// Step 3: Setup local path
	localPath, pathInfo, err := setupLocalPath(localPathArg)
	require.NoError(t, err)
	assert.False(t, pathInfo.IsDir())
	
	// Step 4: Parse S3 path
	bucket, keyPrefix, err := parseS3Path(s3Path)
	require.NoError(t, err)
	assert.Equal(t, "test-bucket", bucket)
	assert.Equal(t, "test-prefix", keyPrefix)
	
	// Update config with parsed values
	config.Bucket = bucket
	config.KeyPrefix = keyPrefix
	config.LocalPath = localPath
	
	// Step 5: Create app
	ctx := context.Background()
	app, err := createApp(ctx, config, localPath, pathInfo.IsDir())
	require.NoError(t, err)
	
	// Verify app configuration
	assert.Equal(t, localPath, app.localPath)
	assert.Equal(t, "test-bucket", app.bucket)
	assert.Equal(t, "test-prefix", app.keyPrefix)
	assert.Equal(t, types.StorageClassStandard, app.storageClass)
	assert.False(t, app.isDir)
	
	// We don't call app.run() as it would start a long-running process
	// Instead, we've verified that all the setup steps work correctly
}
