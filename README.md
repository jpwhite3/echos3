# EchoS3

EchoS3 is a simple, efficient command-line tool written in Go that watches a local file or directory for changes and automatically uploads them to an AWS S3 bucket in real-time.

## Features
- **Real-time Sync**: Uses fsnotify to instantly detect file creations, writes, and deletions.

- **S3 Integration**: Seamlessly uploads changed files to your specified S3 bucket and key prefix.

- **Optional Deletion**: Sync local deletions to S3 with the --delete flag.

- **Configurable Storage Class**: Defaults to S3 Intelligent-Tiering, but allows you to specify any other storage class (e.g., STANDARD, GLACIER).

- **Cross-Platform**: Built with Go, it runs on macOS, Linux, and Windows.

- **Easy Installation**: Available via Homebrew for macOS users.

## Installation

### macOS (with Homebrew)
The recommended way to install on macOS is via Homebrew.

**Tap the repository:**

`brew tap your-github-username/echos3`

**Install EchoS3:**

`brew install echos3`

### From Source (All Platforms)

If you have Go installed, you can build EchoS3 from the source.

**Clone the repository:**
```
git clone https://github.com/your-github-username/echos3.git
cd echos3
```

**Build and install:**

`make install`

This will place the echos3 binary in your Go bin directory.

### From GitHub Releases

You can also download a pre-compiled binary for your operating system directly from the Releases page.

**Usage**

The basic command structure is:

`echos3 [local_path] [s3_uri] [flags]`

Examples
1. Watch a directory and upload to an S3 bucket:

    This will watch the ~/Documents/work-files directory and upload any changes to s3://my-backup-bucket/work/.

    `echos3 ~/Documents/work-files s3://my-backup-bucket/work/`

2. Sync deletions to S3:

    If a file is deleted locally, it will also be deleted from S3.

    `echos3 ./project-a s3://my-backup-bucket/projects/a --delete`

3. Specify a different storage class:

    Upload files using the STANDARD_IA (Standard-Infrequent Access) storage class.

    `echos3 ./important-docs s3://my-archive/docs --storage-class STANDARD_IA`

4. Get the current version:

    `echos3 --version`

## Contributing

Contributions are welcome! Please see the CONTRIBUTING.md file for guidelines on how to contribute to the project, including the branching strategy and pull request process.

## License
This project is licensed under the MIT License. See the LICENSE file for details.