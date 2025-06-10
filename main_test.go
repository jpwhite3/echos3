package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	defer os.RemoveAll(tmpDir)

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
func newTestApp(t *testing.T, deleteFlag bool) (*App, *MockS3Uploader, string) {
	tmpDir := t.TempDir()
	mockUploader := newMockS3Uploader()

	app := &App{
		uploader:     mockUploader,
		localPath:    tmpDir,
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
	// Create a dummy watcher, we don't need it to do anything for this test
	watcher, err := fsnotify.NewWatcher()
	require.NoError(t, err)
	defer watcher.Close()

	t.Run("Create file should trigger upload", func(t *testing.T) {
		app, mockUploader, tmpDir := newTestApp(t, false)
		testFile := filepath.Join(tmpDir, "newfile.txt")
		require.NoError(t, os.WriteFile(testFile, []byte("content"), 0644))

		event := fsnotify.Event{Name: testFile, Op: fsnotify.Create}
		app.handleEvent(context.Background(), event, watcher)

		assert.Contains(t, mockUploader.Uploads, "test-prefix/newfile.txt")
	})

	t.Run("Write to file should trigger upload", func(t *testing.T) {
		app, mockUploader, tmpDir := newTestApp(t, false)
		testFile := filepath.Join(tmpDir, "writefile.txt")
		require.NoError(t, os.WriteFile(testFile, []byte("content"), 0644))

		event := fsnotify.Event{Name: testFile, Op: fsnotify.Write}
		app.handleEvent(context.Background(), event, watcher)

		assert.Contains(t, mockUploader.Uploads, "test-prefix/writefile.txt")
	})

	t.Run("Remove file should trigger delete if flag is set", func(t *testing.T) {
		app, mockUploader, tmpDir := newTestApp(t, true) // delete = true
		testFile := filepath.Join(tmpDir, "delete.txt")

		event := fsnotify.Event{Name: testFile, Op: fsnotify.Remove}
		app.handleEvent(context.Background(), event, watcher)

		assert.Contains(t, mockUploader.Deletes, "test-prefix/delete.txt")
		assert.Empty(t, mockUploader.Uploads)
	})

	t.Run("Remove file should NOT trigger delete if flag is not set", func(t *testing.T) {
		app, mockUploader, tmpDir := newTestApp(t, false) // delete = false
		testFile := filepath.Join(tmpDir, "delete.txt")

		event := fsnotify.Event{Name: testFile, Op: fsnotify.Remove}
		app.handleEvent(context.Background(), event, watcher)

		assert.Empty(t, mockUploader.Deletes)
	})

	t.Run("Create directory should be added to watcher, not uploaded", func(t *testing.T) {
		app, mockUploader, tmpDir := newTestApp(t, false)
		testDir := filepath.Join(tmpDir, "newdir")
		require.NoError(t, os.Mkdir(testDir, 0755))

		event := fsnotify.Event{Name: testDir, Op: fsnotify.Create}
		app.handleEvent(context.Background(), event, watcher)

		assert.Empty(t, mockUploader.Uploads, "Directories should not be uploaded")
	})

	t.Run("Quick create/delete should be handled gracefully", func(t *testing.T) {
		app, mockUploader, tmpDir := newTestApp(t, true)
		testFile := filepath.Join(tmpDir, "temp.txt")
		// The file does not exist, simulating a fast deletion after a create event
		event := fsnotify.Event{Name: testFile, Op: fsnotify.Create}
		app.handleEvent(context.Background(), event, watcher)

		// It should be treated as a removal
		assert.Contains(t, mockUploader.Deletes, "test-prefix/temp.txt")
		assert.Empty(t, mockUploader.Uploads)
	})
}

func TestApp_handleUpload_Errors(t *testing.T) {
	t.Run("Fails when file cannot be opened", func(t *testing.T) {
		app, mockUploader, tmpDir := newTestApp(t, false)
		nonExistentFile := filepath.Join(tmpDir, "ghost.txt")

		app.handleUpload(context.Background(), nonExistentFile, "test-prefix/ghost.txt")

		assert.Empty(t, mockUploader.Uploads, "Upload should not be attempted if file doesn't exist")
	})

	t.Run("Fails when S3 upload returns an error", func(t *testing.T) {
		app, mockUploader, tmpDir := newTestApp(t, false)
		mockUploader.UploadErr = errors.New("S3 is down")
		testFile := filepath.Join(tmpDir, "upload-fail.txt")
		require.NoError(t, os.WriteFile(testFile, []byte("content"), 0644))

		app.handleUpload(context.Background(), testFile, "test-prefix/upload-fail.txt")

		// We can't assert on the log output easily, but we can ensure no successful upload was recorded
		// In a real scenario, you'd check logs or metrics.
		assert.Empty(t, mockUploader.Uploads)
	})
}

func TestApp_handleRemove_Errors(t *testing.T) {
	t.Run("Fails when S3 delete returns an error", func(t *testing.T) {
		app, mockUploader, _ := newTestApp(t, true)
		mockUploader.DeleteErr = errors.New("S3 is down")

		app.handleRemove(context.Background(), "test-prefix/delete-fail.txt")
		assert.Empty(t, mockUploader.Deletes)
	})
}

func TestApp_run_Errors(t *testing.T) {
	t.Run("Fails when watcher cannot be created", func(t *testing.T) {
		// This is hard to test without mocking fsnotify, which is complex.
		// This test is more of a placeholder for the concept.
		// In a real-world scenario, you might inject the watcher dependency.
	})

	t.Run("Fails when initial directory scan fails", func(t *testing.T) {
		app, _, _ := newTestApp(t, false)
		app.localPath = "/path/that/does/not/exist"

		err := app.run(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "error during initial directory scan")
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
