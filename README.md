# gh-deployments

A small CLI tool to list and approve/reject GitHub Actions pending deployments for a repository.

## Installation
Install as a GitHub CLI extension:

```bash
gh extension install lucassarten/gh-deployments
```

## Configuration

- `GITHUB_TOKEN` (env): required. A personal access token with repo/workflow permissions.

- `repo` (flag/variable): If not provided, the tool attempts to detect the current repository via local git context.

## Run / Usage

After installing as a GitHub CLI extension you can run the commands with `gh deployments [command]`.

Examples:

```bash
gh deployments review
```

When listing pending workflows the tool will display candidates like:

`<workflow title> - <actor> - <dd/mm/yy hh:mm:ss>` (e.g. `Deploy site - octocat - 02/11/25 14:05:06`)

Select a workflow and then choose to `Approve` or `Reject` the pending deployment.

## Contributing

Contributions are welcome. Open issues or PRs against the `main` branch.

## License

This project is licensed under the terms in `LICENSE`.
