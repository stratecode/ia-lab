# Getting Started

Step-by-step guide to bootstrap the project from scratch on a new machine.

## 1. Install Ansible

On macOS:

```bash
brew install ansible
```

On Ubuntu/Debian:

```bash
sudo apt update && sudo apt install -y ansible
```

Install the required collection:

```bash
ansible-galaxy collection install community.general
```

## 2. Generate SSH keys

Create a dedicated keypair for Ansible to connect to the target host:

```bash
mkdir -p ssh
ssh-keygen -t ed25519 -C "ia-lab-ansible" -f ssh/lab -N ""
```

This produces:
- `ssh/lab` — private key (git-ignored)
- `ssh/lab.pub` — public key (git-ignored)

Copy the public key to the target host:

```bash
ssh-copy-id -i ssh/lab.pub <user>@<host>
```

Verify connectivity:

```bash
ssh -i ssh/lab <user>@<host> "hostname"
```

## 3. Create the vault password file

Choose a strong password and store it in a file that Ansible will read automatically:

```bash
mkdir -p ~/.config/ia-lab
openssl rand -base64 32 > ~/.config/ia-lab/ansible-vault-pass
chmod 600 ~/.config/ia-lab/ansible-vault-pass
```

You can use any path — just set it in your `.env`:

```bash
ANSIBLE_VAULT_PASSWORD_FILE=~/.config/ia-lab/ansible-vault-pass
```

## 4. Create the encrypted vault file

```bash
ansible-vault create group_vars/vault.yml
```

This opens your editor. Add the required secrets:

```yaml
# Grafana
vault_grafana_admin_password: "your-grafana-password"

# Alertmanager (optional — leave empty to disable Slack alerts)
vault_alertmanager_slack_webhook_url: ""

# AWS credentials for Route53 DDNS and Let's Encrypt
vault_aws_cli_profiles:
  route53-dns:
    aws_access_key_id: "AKIA..."
    aws_secret_access_key: "..."
    region: "us-east-1"
```

Save and close. The file is now encrypted.

Useful vault commands:

```bash
# View contents
ansible-vault view group_vars/vault.yml

# Edit contents
ansible-vault edit group_vars/vault.yml

# Re-encrypt with a new password
ansible-vault rekey group_vars/vault.yml
```

## 5. Configure environment variables

```bash
cp .env.example .env
```

Edit `.env` with your actual values:
- Host IP and hostname
- Domains for Cockpit and Grafana
- Route53 zone and record names
- llama.cpp model configuration
- WireGuard subnet (if changing defaults)
- `ANSIBLE_VAULT_PASSWORD_FILE` path

## 6. Prepare the target host

The target must be a fresh Ubuntu Server (22.04 or 24.04) with:

- SSH server running
- A user account matching `LAB_USER` in your `.env`
- The public key from step 2 in `~/.ssh/authorized_keys`
- Sudo access without password (or configure `ansible_become_pass` in vault)

To enable passwordless sudo on the target:

```bash
echo "<user> ALL=(ALL) NOPASSWD:ALL" | sudo tee /etc/sudoers.d/ansible
sudo chmod 440 /etc/sudoers.d/ansible
```

## 7. Test connectivity

```bash
set -a && source .env && set +a
ansible all -m ping
```

Expected output:

```
lab | SUCCESS => {
    "ping": "pong"
}
```

## 8. Run the playbook

The playbook requires environment variables from `.env` to be exported in your shell session. Always source the file before running any Ansible command:

```bash
set -a && source .env && set +a
ansible-playbook playbooks/bootstrap.yml
```

> **Important:** `set -a` makes every variable in `.env` automatically exported. Without this, Ansible's `lookup('env', ...)` calls will return empty values and the playbook will use defaults (which may not match your setup).

For a safe first run, use check mode to preview changes without applying them:

```bash
set -a && source .env && set +a
ansible-playbook playbooks/bootstrap.yml --check --diff
```

### First run considerations

The first execution takes longer because:
- llama.cpp is compiled from source (~5 minutes)
- AI models are downloaded from HuggingFace (~4GB for Code, ~2GB for Planner, ~1GB for Utility, ~140MB for Embeddings)
- The healthcheck waits up to 7.5 minutes per instance for model loading

Subsequent runs are idempotent and complete in under a minute.

### Running a single role

To run only the llama.cpp role (or any other):

```bash
set -a && source .env && set +a
ansible-playbook playbooks/bootstrap.yml --start-at-task="Install llama.cpp build dependencies"
```

## 9. Post-install verification

After a successful run, verify all services are operational:

```bash
# Check all llama.cpp instances respond
curl -s -H "Authorization: Bearer $LAB_LLAMA_CODE_API_KEY" http://<host>:8080/v1/models
curl -s -H "Authorization: Bearer $LAB_LLAMA_PLANNER_API_KEY" http://<host>:8082/v1/models
curl -s -H "Authorization: Bearer $LAB_LLAMA_UTILITY_API_KEY" http://<host>:8083/v1/models
curl -s -H "Authorization: Bearer $LAB_LLAMA_EMBEDDINGS_API_KEY" http://<host>:8084/v1/models

# Test inference on the code model
curl -s -X POST -H "Authorization: Bearer $LAB_LLAMA_CODE_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"coder","messages":[{"role":"user","content":"Say hi"}],"max_tokens":10}' \
  http://<host>:8080/v1/chat/completions

# Test embeddings endpoint
curl -s -X POST -H "Authorization: Bearer $LAB_LLAMA_EMBEDDINGS_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"input":"hello world"}' \
  http://<host>:8084/v1/embeddings

# Check WireGuard
ssh -i ssh/lab <user>@<host> "sudo wg show"

# Check Grafana (via VPN or LAN)
curl -k https://<observability_domain>/api/health
```

### Verifying service status on the host

```bash
# Check all llama.cpp services
ssh -i ssh/lab <user>@<host> "systemctl status llama-cpp-code llama-cpp-planner llama-cpp-utility llama-cpp-embeddings --no-pager"

# Check monitoring timer
ssh -i ssh/lab <user>@<host> "systemctl status llama-cpp-monitor.timer --no-pager"

# View instance logs
ssh -i ssh/lab <user>@<host> "tail -20 /var/log/llama-cpp/llama-cpp-code.log"
```

## Troubleshooting

### "Permission denied" on SSH

Ensure the public key is in the target's `authorized_keys` and the private key permissions are correct:

```bash
chmod 600 ssh/lab
```

### "Vault password file not found"

Check that `ANSIBLE_VAULT_PASSWORD_FILE` in your `.env` points to an existing file:

```bash
ls -la $(grep ANSIBLE_VAULT_PASSWORD_FILE .env | cut -d= -f2)
```

### "Host unreachable"

Verify the hostname/IP in your `.env` matches the target and that SSH port 22 is open:

```bash
ssh -i ssh/lab -o ConnectTimeout=5 <user>@<host> "echo ok"
```

### Vault decryption errors

If you see `Decryption failed`, the vault password file content doesn't match what was used to encrypt `group_vars/vault.yml`. Re-create the vault with the current password:

```bash
ansible-vault decrypt group_vars/vault.yml  # with old password
ansible-vault encrypt group_vars/vault.yml  # with new password
```

### llama.cpp services fail to start

Check the application logs (not journalctl — stdout/stderr go to the log file):

```bash
ssh -i ssh/lab <user>@<host> "tail -30 /var/log/llama-cpp/llama-cpp-code.log"
```

Common causes:
- **"file not found in repository"** — The `--hf-repo` value doesn't match an available GGUF file. Check the model name and quantization in `.env`.
- **"Failed to set up standard output: No such file or directory"** — The log directory `/var/log/llama-cpp/` doesn't exist. Re-run the playbook to create it.
- **OOM killed (exit code 137)** — The model exceeds the `MemoryMax` limit. Increase `model_size_mb` in `group_vars/all.yml` or reduce `ctx_size`.
- **Port already in use** — Another instance or process is using the same port. Check with `ss -tlnp | grep <port>`.

### Healthcheck times out during playbook

The playbook waits up to 7.5 minutes (90 retries × 5s) per instance. If it times out:

```bash
# Check if the service is actually running
ssh -i ssh/lab <user>@<host> "systemctl is-active llama-cpp-code"

# Check if it's still downloading the model
ssh -i ssh/lab <user>@<host> "tail -5 /var/log/llama-cpp/llama-cpp-code.log"
```

On first run, large models (7B) can take 10+ minutes to download depending on network speed. If the healthcheck fails, re-run the playbook — it will pick up where it left off.

### Thread over-subscription warning

If you see "Thread over-subscription: total N exceeds budget 12", the sum of all instance `threads` values exceeds the configured thread budget. This is a non-fatal warning. To fix it, reduce thread counts in `.env` (e.g., `LAB_LLAMA_PLANNER_THREADS=2`).
