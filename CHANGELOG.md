# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Concurrent file uploads using Go goroutines for improved performance
- New `--concurrency` flag to control the maximum number of concurrent uploads
- Automatic detection of optimal concurrency based on system resources

### Changed
- Improved upload handling with a worker pool pattern
- Better resource management for large directory uploads

## [1.0.0] - Initial Release

### Added
- Real-time file watching with fsnotify
- Automatic uploads to S3 when files change
- Support for watching directories or single files
- Optional deletion sync with `--delete` flag
- Configurable S3 storage class with `--storage-class` flag