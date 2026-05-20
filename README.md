# prober

<a name="readme-top"></a>

<div align="center">
<img height="535" alt="ymr-logo" src="./assets/prober.png" />

<br />

<p align="center">
<strong>prober</strong> is a lightweight HTTP(S) uptime probe CLI for CI and monitoring. It reads a YAML manifest, runs probes in parallel, evaluates CEL assertions, publishes results, and can fail the process when any target is down.

<br />

<a href="https://github.com/lnrdll/prober/issues/new?labels=bug&template=bug_report.md">Report Bug</a>
·
<a href="https://github.com/lnrdll/prober/issues/new?labels=enhancement&template=feature_request.md">Request Feature</a>
</p>
</div>

## Features

- YAML-driven target definitions
- Parallel HTTP(S) probing
- CEL-based assertions
- Per-target `timeout` and `retries`
- Default `User-Agent: prober`
- Summary output for CI logs
- `lint` command for manifest and assertion validation
- Output publishers for stdout JSON, file logs, Datadog StatsD, and GCP StatsD

## Installation

### Go install

```bash
go install github.com/lnrdll/prober@latest
```

### Mise

```bash
[tools]
"github:lnrdll/prober" = "latest"  
```

### Build locally

```bash
go build -o prober .
```

### Run without building

```bash
go run main.go run -c targets.yaml
```

## Usage

`prober` uses subcommands:

```bash
prober run -c targets.yaml
prober lint -c targets.yaml
```

Root help:

```bash
go run main.go
```

Run command help:

```bash
go run main.go run --help
```

## Manifest format

Example `targets.yaml`:

```yaml
targets:
  - name: homepage
    url: https://example.com
    method: GET
    timeout: 10
    retries: 2
    headers:
      X-Env: prod
    assertions:
      - status == 200
      - latency_ms < 2000
      - body.contains("Example Domain")
    tags:
      service: web
      environment: prod

  - name: tls-check
    url: https://example.com
    assertions:
      - status >= 200 && status < 400
      - ssl_days_left > 7
```

## Target fields

Each target supports:

- `name`: unique target name, required
- `url`: HTTP(S) URL, required
- `method`: optional uppercase HTTP method
- `timeout`: optional per-target timeout in seconds, default `60`
- `retries`: optional retry count, default `0`
- `disabled`: skip this target
- `ssl_skip_verify`: disable TLS verification
- `headers`: request headers
- `body`: request body
- `assertions`: CEL expressions
- `tags`: metadata forwarded to publishers

Validation rules include:

- at least one target is required
- `name` must be unique
- `url` must be valid
- `method` must be uppercase and supported
- `timeout` and `retries` must be `>= 0`
- header names cannot be empty

## Assertions

Assertions use CEL and are evaluated against these fields:

- `status`
- `body`
- `latency_ms`
- `ssl_days_left`
- `ssl_issuer`
- `ssl_subject`
- `ssl_dns_names`
- `headers`

If no assertions are provided, the default is:

```cel
status >= 200 && status < 400
```

Examples:

```cel
status == 200
latency_ms < 1000
headers["Content-Type"].contains("application/json")
body.contains("ok")
ssl_days_left > 14
```

## Commands

### `prober run`

Run probes from a manifest:

```bash
prober run -c targets.yaml
```

Useful flags:

- `-c, --config`: path to manifest YAML, required
- `--summary`: print run summary
- `--fail-on-target-failure`: exit non-zero when any target fails
- `--stdout`: emit structured JSON logs to stdout
- `--log-file <path>`: append structured JSON logs to a file
- `--datadog-statsd <addr>`: publish metrics to Datadog StatsD
- `--gcp-statsd <addr>`: publish metrics to GCP Ops Agent StatsD

Examples:

```bash
prober run -c targets.yaml --summary
prober run -c targets.yaml --summary --fail-on-target-failure
prober run -c targets.yaml --stdout
prober run -c targets.yaml --log-file prober.log
prober run -c targets.yaml --datadog-statsd 127.0.0.1:8125
```

Summary output looks like:

```text
Summary: total=2 passed=1 failed=1 skipped=0 duration=1.234s
FAILED https://example.com status=500 reason=status == 200
```

### `prober lint`

Validate manifest structure and CEL assertions without probing:

```bash
prober lint -c targets.yaml
```

## Output publishers

If no publisher is configured, stdout JSON logging is enabled by default.

### Stdout

```bash
prober run -c targets.yaml --stdout
```

### File

```bash
prober run -c targets.yaml --log-file prober.log
```

### Datadog StatsD

```bash
prober run -c targets.yaml --datadog-statsd 127.0.0.1:8125
```

### GCP StatsD

```bash
prober run -c targets.yaml --gcp-statsd 127.0.0.1:8125
```

## Behavior notes

- Requests do not follow redirects.
- Requests to cloud metadata hosts are blocked.
- Response bodies are truncated to 1024 bytes before being stored in results.
- A `Host` header override is supported.
- Disabled targets are counted as skipped.

## Development

Run tests:

```bash
go test ./...
```

Using `mise`:

```bash
mise run test
mise run lint
mise run build
mise run build-linux
```

## License

See [LICENSE](./LICENSE).
