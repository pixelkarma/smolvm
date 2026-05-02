# smolvm

`smolvm` is a lightweight admin for running multiple isolated `shelley` instances as Docker containers. It is for development only: the AI is intentionally given root access inside its container, and the system is meant to be ephemeral, so host and instance hardening are not design goals.

Each instance gets:
- its own container
- its own writable disk-backed workspace
- its own resource limits
- a private `shelley` UI behind the admin
- a public app port for whatever web server the instance runs

## Install

Run on a fresh Alpine, Ubuntu, or Debian server as `root`:

```sh
git clone https://github.com/pixelkarma/smolvm
cd smolvm
./build.sh
```

Optional overrides:

```sh
SMOLVM_ADMIN_PASSWORD='change-this' \
SMOLVM_PUBLIC_HOST='1.2.3.4' \
SMOLVM_SYSTEM_KEY='/root/.openai' \
./build.sh
```

## Additional manual setup

You still need to provide:

- an OpenAI API key file on the host
- a real admin password if you do not want the default

Default key path:

```sh
/root/.openai
```

Accepted file contents:

```sh
sk-...
```

or:

```sh
OPENAI_API_KEY=sk-...
```

Default admin URL:

```text
http://SERVER_IP:8090/login
```

Default admin password if not overridden:

```text
changeme
```

Change it immediately in the admin UI.

## How it works

- The admin runs on port `8090`.
- `shelley` is not meant to be exposed directly.
- Each instance gets a private internal `shelley` port, and the admin proxies it after login.
- Each instance also gets a public app port, starting at `8100` and incrementing.

Example:

- admin: `http://SERVER_IP:8090`
- instance app A: `http://SERVER_IP:8100`
- instance app B: `http://SERVER_IP:8101`

Inside the container, `shelley` is told which app port to use. If it starts a web app, it should bind to:

```text
0.0.0.0:$PROJECT_WEB_PORT
```

## Capabilities

- create, start, stop, and delete `shelley` instances
- set per-instance RAM, CPU, disk, app port, API key path, and prompt
- inject a global prompt plus an instance prompt
- mount a persistent workspace and `shelley` state per instance
- expose the instance app port directly
- keep the `shelley` UI behind the admin login

## Limitations

- Instances are Docker containers, not full VMs.
- Container root is not the same isolation boundary as a real VM.
- The admin auth is intentionally simple: one shared password.
- The `shelley` UI is proxied under the admin, so upstream UI changes may require proxy adjustments.
- The build script installs system packages and services; run it only on a host intended for `smolvm`.

## Service locations

Alpine:

- config: `/etc/conf.d/smolvm`
- service: `/etc/init.d/smolvm`

Ubuntu/Debian:

- config: `/etc/default/smolvm`
- service: `/etc/systemd/system/smolvm.service`

Runtime files:

- binaries: `/opt/smolvm/bin`
- data: `/opt/smolvm/data`
