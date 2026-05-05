# smolvm

`smolvm` is a lightweight admin for running multiple isolated coding-agent instances as Alpine QEMU VMs. It is for development only: the AI is intentionally given root access inside its guest, and the system is meant to be ephemeral, so host and instance hardening are not design goals.

Each instance gets:
- its own VM
- its own writable disk-backed workspace
- its own resource limits
- a private agent UI behind the admin
- a public app port for whatever web server the instance runs
- a QEMU VM boundary instead of a shared-container boundary

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
SMOLVM_DEFAULT_OPENAI_API_KEY='sk-...' \
./build.sh
```

## Additional manual setup

You still need to provide:

- an OpenAI API key
- a real admin password if you do not want the default

The installer writes the runtime config to:

```sh
~/.smolvm/smolvm.config.json
```

The important fields are:

```json
{
  "listen_addr": ":8090",
  "data_dir": "~/.smolvm/data",
  "agent_binary_path": "~/.smolvm/bin/smolagent-linux-aarch64",
  "public_host": "SERVER_IP",
  "default_openai_api_key": "sk-...",
  "admin_password": "changeme",
  "qemu_binary_path": "/usr/bin/qemu-system-x86_64",
  "template_image_path": "~/.smolvm/assets/alpine-template.ext4"
}
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
- The private agent UI is not meant to be exposed directly.
- Each instance gets a private internal agent port, and the admin proxies it after login.
- Each instance also gets a public app port, starting at `8100` and incrementing.
- The agent runtime inside each guest also reads `/root/.smolvm/smolvm.config.json`.
- Each VM uses QEMU user-mode networking with host port forwards for the app, agent, and debug SSH ports.

Example:

- admin: `http://SERVER_IP:8090`
- instance app A: `http://SERVER_IP:8100`
- instance app B: `http://SERVER_IP:8101`

Inside the guest, the agent is told which app port to use. If it starts a web app, it should bind to:

```text
0.0.0.0:$PROJECT_WEB_PORT
```

## Capabilities

- create, start, stop, and delete agent instances
- set per-instance RAM, CPU, disk, app port, API key override, and prompt
- inject a global prompt plus an instance prompt
- persist workspace and agent state inside each VM disk image
- expose the instance app port directly
- keep the private agent UI behind the admin login

## Limitations

- Instances run as QEMU VMs. Linux hosts use KVM when available; other hosts fall back to emulation.
- Local macOS development can run the same VM backend through QEMU, but it will be slower than Linux KVM.
- The admin auth is intentionally simple: one shared password.
- The private agent UI is proxied under the admin.
- The build script installs system packages and services; run it only on a host intended for `smolvm`.

## Service locations

- config: `~/.smolvm/smolvm.config.json`
- binaries: `~/.smolvm/bin`
- data: `~/.smolvm/data`

- Alpine service: `/etc/init.d/smolvm`
- Ubuntu/Debian service: `/etc/systemd/system/smolvm.service`
