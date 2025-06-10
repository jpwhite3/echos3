package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockS3Uploader is a mock implementation of the S3Uploader interface for testing.
type MockS3Uploader struct {
	Uploads   map[string]*s3.PutObjectInput
	Deletes   map[string]*s3.DeleteObjectInput
	UploadErr error
	DeleteErr error
}

// newMockS3Uploader creates a new mock uploader for a test.
func newMockS3Uploader() *MockS3Uploader {
	return &MockS3Uploader{
		Uploads: make(map[string]*s3.PutObjectInput),
		Deletes: make(map[string]*s3.DeleteObjectInput),
	}
}

// Upload mocks the S3 upload operation.
func (m *MockS3Uploader) Upload(_ context.Context, input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
	if m.UploadErr != nil {
		return nil, m.UploadErr
	}
	m.Uploads[*input.Key] = input
	return &s3.PutObjectOutput{}, nil
}

// DeleteObject mocks the S3 delete operation.
func (m *MockS3Uploader) DeleteObject(_ context.Context, input *s3.DeleteObjectInput) (*s3.DeleteObjectOutput, error) {
	if m.DeleteErr != nil {
		return nil, m.DeleteErr
	}
	m.Deletes[*input.Key] = input
	return &s3.DeleteObjectOutput{}, nil
}

// TestParseS3Path tests the S3 path parsing logic.
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
		{"Invalid scheme", "http://my-bucket/path", "", "", true},
		{"No scheme", "my-bucket/path", "", "", true},
		{"No bucket", "s3://", "", "", true},
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

// TestHandleEvent_Upload tests the upload functionality on a file write event.
func TestHandleEvent_Upload(t *testing.T) {
	// Setup a temporary directory and file
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	err := os.WriteFile(testFile, []byte("hello"), 0644)
	require.NoError(t, err)

	mockUploader := newMockS3Uploader()
	app := &App{
		uploader:     mockUploader,
		localPath:    tmpDir,
		bucket:       "test-bucket",
		keyPrefix:    "test-prefix",
		delete:       false,
		storageClass: types.StorageClassStandard,
	}

	// Create a dummy watcher, we don't need it to do anything for this test
	watcher, err := fsnotify.NewWatcher()
	require.NoError(t, err)
	defer watcher.Close()

	// Simulate a write event
	event := fsnotify.Event{Name: testFile, Op: fsnotify.Write}
	app.handleEvent(context.Background(), event, watcher)

	// Assertions
	expectedKey := "test-prefix/test.txt"
	require.Contains(t, mockUploader.Uploads, expectedKey)
	assert.Equal(t, "test-bucket", *mockUploader.Uploads[expectedKey].Bucket)
	assert.Equal(t, types.StorageClassStandard, mockUploader.Uploads[expectedKey].StorageClass)
}

// TestHandleEvent_Delete tests the delete functionality on a file remove event.
func TestHandleEvent_Delete(t *testing.T) {
	tmpDir := t.TempDir()
	mockUploader := newMockS3Uploader()
	app := &App{
		uploader:     mockUploader,
		localPath:    tmpDir,
		bucket:       "test-bucket",
		keyPrefix:    "test-prefix",
		delete:       true, // Enable deletion
		storageClass: types.StorageClassStandard,
	}

	// Simulate a remove event
	removedFile := filepath.Join(tmpDir, "deleted.txt")
	event := fsnotify.Event{Name: removedFile, Op: fsnotify.Remove}
	app.handleEvent(context.Background(), event, nil) // watcher can be nil for this test

	// Assertions
	expectedKey := "test-prefix/deleted.txt"
	require.Contains(t, mockUploader.Deletes, expectedKey)
	assert.Equal(t, "test-bucket", *mockUploader.Deletes[expectedKey].Bucket)
}
