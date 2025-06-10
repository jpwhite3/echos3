# Contributing to EchoS3

First off, thank you for considering contributing to EchoS3! Your help is greatly appreciated. Following these guidelines helps to communicate that you respect the time of the developers managing and developing this open-source project.

## Code of Conduct

This project and everyone participating in it is governed by the [EchoS3 Code of Conduct](CODE_OF_CONDUCT.md). By participating, you are expected to uphold this code. Please report unacceptable behavior.

## How Can I Contribute?

### Reporting Bugs

If you find a bug, please ensure the bug was not already reported by searching on GitHub under [Issues](https://github.com/jpwhite3/echos3/issues). If you're unable to find an open issue addressing the problem, [open a new one](https://github.com/jpwhite3/echos3/issues/new). Be sure to include a title and clear description, as much relevant information as possible, and a code sample or an executable test case demonstrating the expected behavior that is not occurring.

### Suggesting Enhancements

If you have an idea for an enhancement, please open an issue to discuss the feature before you begin working on it. This allows us to coordinate efforts and ensure the feature aligns with the project's goals.

## Development Workflow

We use a simple Git branching model for this project:

- `main`: This is the primary release branch. It is protected, and code is only merged into it from the `develop` branch. All code on `main` is considered stable and is used to create official releases.

- `develop`: This is the pre-release or integration branch. All feature branches are merged into `develop` first.

- `feature/\*`: All new work (features, bug fixes) should be done on a feature branch. Branch names should be descriptive (e.g., `feature/add-retry-logic`, `issue/123`).

### Pull Request Process

1. Fork the repository and create your feature branch from `develop`.

2. Make your changes. Ensure you add or update tests as appropriate.

3. Make sure your code lints and passes all tests by running `make test` and `make lint`.

4. Commit your changes using a descriptive commit message that follows the [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) specification. This is important as our release changelogs are generated from these messages.

5. Push your feature branch to your fork.

6. Open a pull request from your feature branch to the `develop` branch of the main repository.

7. The CI pipeline will automatically run. All checks must pass before a maintainer can review and merge your PR.

Thank you for your contribution!
