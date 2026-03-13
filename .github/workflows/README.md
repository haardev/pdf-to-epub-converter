# GitHub Actions Workflows

## `deploy.yml`

**Triggers on**: Push to `main` branch

**What it does**:
1. Connects to your Azure VM via SSH
2. Pulls latest code from GitHub
3. Rebuilds and restarts Docker containers
4. Completes in ~2 minutes

**Requirements**:
- Add these secrets to your GitHub repo (Settings → Secrets):
  - `VM_HOST`: Your VM's public IP
  - `VM_USER`: SSH username (e.g., `azureuser`)
  - `VM_SSH_KEY`: Private SSH key (generated locally)

See `../DEPLOYMENT.md` for full setup instructions.
