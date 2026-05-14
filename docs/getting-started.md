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

```bash
ansible-playbook playbooks/bootstrap.yml
```

For a safe first run, use check mode to preview changes:

```bash
ansible-playbook playbooks/bootstrap.yml --check --diff
```

## 9. Post-install verification

After a successful run:

```bash
# Check WireGuard
ssh -i ssh/lab <user>@<host> "sudo wg show"

# Check llama.cpp
curl http://<host>:8080/v1/models

# Check Grafana (via VPN or LAN)
curl -k https://<observability_domain>/api/health
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
