# smolvm

`smolvm` is a lightweight admin for running multiple isolated coding-agent instances as Alpine QEMU VMs. It is for development only: the guest runs as `root` on purpose, and the whole system is designed to be disposable.

Each instance gets:
- its own QEMU VM
- its own writable disk image cloned from a golden Alpine image
- a private agent UI behind the admin
- a browser terminal into the VM
- a public web port for the app running inside the guest

## Requirements

`build.sh` does not install host packages for you anymore. Have these available first:

```sh
curl
expect
go
qemu-img
qemu-system-x86_64
ssh-keygen
```

On Linux, KVM is optional but preferred. Without it, QEMU falls back to emulation.

## Install

```sh
git clone https://github.com/pixelkarma/smolvm
cd smolvm
./build.sh
```

`build.sh`:
- builds `smolvm-admin` for the host
- cross-builds `smolagent` for Linux `amd64`
- downloads Alpine installer assets if they are missing
- creates or reuses `~/.smolvm/assets/alpine-golden.qcow2`
- writes `~/.smolvm/smolvm.config.json`

If a golden image already exists, the script asks whether to reuse it or rebuild it.

Optional overrides:

```sh
SMOLVM_ADMIN_PASSWORD='change-this' \
SMOLVM_PUBLIC_HOST='1.2.3.4' \
SMOLVM_DEFAULT_OPENAI_API_KEY='sk-...' \
./build.sh
```

## Run the admin

After the build finishes, start the admin manually:

```sh
~/.smolvm/bin/smolvm-admin --config ~/.smolvm/smolvm.config.json
```

Default URL:

```text
http://SERVER_IP:8090/login
```

Default password if you did not override it:

```text
changeme
```

## Config

Host config lives at:

```sh
~/.smolvm/smolvm.config.json
```

Typical host config:

```json
{
  "listen_addr": ":8090",
  "data_dir": "/home/user/.smolvm/data",
  "public_host": "192.168.1.54",
  "default_openai_api_key": "sk-...",
  "admin_password": "changeme",
  "qemu_binary_path": "/usr/bin/qemu-system-x86_64",
  "template_image_path": "/home/user/.smolvm/assets/alpine-golden.qcow2",
  "guest_ssh_key_path": "/home/user/.smolvm/keys/guest-admin"
}
```

## How it works

- The admin runs on port `8090`.
- Each VM gets a copied QCOW2 disk from the golden image.
- The admin launches QEMU daemonized and only tracks whether the VM process is present.
- The private agent UI is proxied through the admin after login.
- Public app ports start at `8100` and increment.
- Guest SSH is forwarded to a host port starting at `10001`.

Current networking model:

- guest `9000` = private `smolagent` UI
- guest `80` = public web app port
- host `127.0.0.1:<agent port>` -> guest `9000`
- host `:<web port>` -> guest `80`
- host `:<ssh port>` -> guest `22`

The guest learns its instance id from QEMU SMBIOS:

```text
/sys/class/dmi/id/product_serial
```

`smolagent` refreshes its config from the admin on every launch by requesting:

```text
http://10.0.2.2:8090/internal/instance-config?instance_id=<id>
```

That is how the guest picks up:
- the effective OpenAI API key
- the global prompt
- the instance prompt

## UI behavior

Each instance row in the admin includes:
- `Agent`
- `Terminal`
- `Web App`
- a `...` menu for `Start`, `Stop`, and `Delete`

The `Active` column is browser-side only:
- `checking` while the browser probes the proxied agent URL
- `active` if the probe succeeds
- `inactive` if it times out or fails

The browser terminal uses local vendored `xterm.js` assets and connects through an admin websocket. It SSHes into the guest over the forwarded guest SSH port.

## Capabilities

- create, start, stop, and delete VM instances
- set per-instance RAM, CPU, disk, web port, API key override, and prompt
- private agent UI behind admin auth
- browser terminal into the guest
- direct public web port exposure for the guest app
- per-instance persistent disk images

## Limitations

- Admin auth is intentionally simple: one shared password
- VM state is tracked by process inspection, not a full orchestrator
- The admin does not validate guest readiness before returning from `Start`
- The system assumes trusted local administration and disposable guests
- The guest app is expected to listen on port `80`

## Important paths

- config: `~/.smolvm/smolvm.config.json`
- binaries: `~/.smolvm/bin`
- guest image assets: `~/.smolvm/assets`
- VM disks and database: `~/.smolvm/data`
- vendored terminal assets: `static/xterm/`
