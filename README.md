# gh-deployments

A small CLI tool to list and approve/reject GitHub Actions pending deployments for a repository.

## Installation
Install as a GitHub CLI extension:

```bash
gh extension install lucassarten/gh-deployments
```

## Configuration

- `GITHUB_TOKEN` (env): required. A personal access token with repo/workflow permissions.

## Run / Usage

After installing as a GitHub CLI extension you can run the commands with `gh deployments [command]`.

```
Usage:
  gh-deployments review [flags]

Flags:
      --action string      Action to perform in non-interactive mode: approve or reject
      --actor string       Filter workflows by actor login
      --env-all            Act on all pending environments for the selected workflow (non-interactive)
      --env-names string   Comma-separated environment names to act on (non-interactive)
      --event string       Filter workflows by event (e.g. push, pull_request)
  -h, --help               help for review
      --order string       Sort order: asc or desc (default "desc")
  -p, --poll               Poll for pending deployments if none are found
      --run int            Workflow run id to act on
      --sort string        Sort by: created_at, actor, name (default "created_at")
      --workflow string    Workflow yaml file name to act on (e.g. deploy.yml)

Global Flags:
  -r, --repo string   Repository to search in (owner/repo)
```

The CLI supports interactive and non-interactive modes for reviewing pending deployments. When specifying the `--action` flag, the tool operates in non-interactive mode, which will only perform actions if the provided flags narrow down the action to a specific workflow run.


Examples:

```bash
gh deployments review
```

> To select from pending deployments interactively.

```bash
gh deployments review --actor <actor-login> --poll
```

> To filter pending deployments by actor login and poll until at least one is found, then select interactively.

```bash
gh deployments review --action approve
```

> Will approve the latest workflow run in the repository if there is only one pending deployment.

```bash
gh deployments review --action approve --env-names plan --workflow deploy.yml
```

> Will approve the `plan` environment for all pending deployments from the `deploy.yml` workflow, if there is only one workflow run pending (otherwise provide `--run`).

```bash
gh deployments review --action approve --env-all --run 19263528744
```

> Will approve all pending environments for the workflow run with ID `19263528744`.

## Contributing

Contributions are welcome. Open issues or PRs against the `main` branch.

## License

This project is licensed under the terms in `LICENSE`.
