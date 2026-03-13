# Azure VM Deployment Guide

## Quick Summary
This guide sets up auto-deploy: **push to GitHub → VM auto-updates**.

---

## Step 1: Create Azure VM

In Azure Portal:

1. **Create VM**
   - Image: `Ubuntu 22.04 LTS` or newer
   - Size: `B1ms` (cheapest) or `B2s` if you want more CPU
   - Region: Choose closest to you

2. **Networking** (Important!)
   - Inbound rules: Allow **SSH (22)** and **HTTP (8080)**

3. **Download SSH key** during creation (save as `vm_key.pem`)

---

## Step 2: SSH Into VM & Setup Docker

```bash
# SSH in (replace YOUR_IP with your VM public IP)
ssh -i vm_key.pem azureuser@YOUR_IP

# Update system
sudo apt update && sudo apt upgrade -y

# Install Docker
sudo apt install -y docker.io docker-compose-plugin git

# Add your user to docker group (so you don't need sudo)
sudo usermod -aG docker $USER
newgrp docker

# Verify Docker works
docker --version
```

---

## Step 3: Clone Repository on VM

```bash
cd /home/azureuser

# Clone your repo
git clone https://github.com/YOUR_USERNAME/pdf-to-epub-converter.git
cd pdf-to-epub-converter

# Create .env file (adjust AI provider as needed)
cp .env.example .env

# For local Ollama (if running on same VM):
nano .env
# Keep defaults: AI_PROVIDER=ollama, DB_URL=postgres://rag:rag@postgres:5432/ragdb

# For Azure AI models:
nano .env
# Change AI_PROVIDER=azure-openai
# Add your Azure credentials
```

---

## Step 4: Test Manual Deployment

```bash
# Make sure you're in the repo directory
cd /home/azureuser/pdf-to-epub-converter

# Start the stack
docker compose up -d --build

# Check health
curl http://localhost:8080/health
# Should return: {"status":"ok"}
```

---

## Step 5: Setup GitHub Actions Auto-Deploy

### 5a. Generate SSH Key for GitHub Actions

On your local machine:

```bash
# Generate a key pair (no passphrase, just press enter)
ssh-keygen -t ed25519 -f github_actions_key -N ""

# You'll have:
# - github_actions_key (private key - goes to GitHub)
# - github_actions_key.pub (public key - goes to VM)
```

### 5b. Add Public Key to VM

On your **VM** (via SSH):

```bash
# Create .ssh directory if needed
mkdir -p ~/.ssh
chmod 700 ~/.ssh

# Add the public key
echo "PASTE_CONTENT_OF_github_actions_key.pub_HERE" >> ~/.ssh/authorized_keys
chmod 600 ~/.ssh/authorized_keys
```

### 5c. Add Secrets to GitHub

In your GitHub repo:

1. Go to **Settings → Secrets and variables → Actions**
2. Create these secrets:

| Secret Name | Value |
|---|---|
| `VM_HOST` | Your VM's public IP (e.g., `20.123.45.67`) |
| `VM_USER` | `azureuser` |
| `VM_SSH_KEY` | Contents of `github_actions_key` (private key) |

---

## Step 6: Test Auto-Deploy

Make a small change and push:

```bash
# On your local machine
git add .
git commit -m "Test deployment"
git push origin main
```

Go to **GitHub → Actions** and watch the workflow run. In ~1-2 minutes:
- ✅ Code pulls to VM
- ✅ Docker containers rebuild
- ✅ App restarts with new code

Check your VM is alive:

```bash
curl http://YOUR_VM_IP:8080/health
```

---

## Step 7: Ingest Your First PDF (on VM)

```bash
# SSH into VM
ssh -i vm_key.pem azureuser@YOUR_IP

# Copy PDF to VM
scp -i vm_key.pem docs/my-paper.pdf azureuser@YOUR_IP:/tmp/

# Run ingest (uses Docker)
docker run --rm \
  --network host \
  --env-file .env \
  -e DB_URL=postgres://rag:rag@localhost:5432/ragdb \
  -v /home/azureuser/pdf-to-epub-converter/docs:/data \
  $(docker build --build-arg APP_PATH=./cmd/ingest -q .) \
  /app/app /data/my-paper.pdf
```

---

## Troubleshooting

### Check Docker containers
```bash
docker ps -a
docker logs pdf-rag-api
docker logs ragdb
```

### Restart manually
```bash
cd /home/azureuser/pdf-to-epub-converter
docker compose down
docker compose up -d --build
```

### View deployment history
Go to GitHub **Actions** tab to see all deployments and their logs.

### SSH issues?
Verify your inbound rules in Azure allow SSH (port 22).

---

## Cost Estimate

- **B1ms VM**: ~$10-15/month
- **Storage (OS + data)**: ~$5/month
- **Bandwidth**: ~$5/month (if low traffic)
- **Total**: ~$20-25/month for basic PoC

If using Azure AI models, add those service costs separately.

---

## Next Steps

- Monitor your app: `docker logs -f pdf-rag-api`
- Set up automated backups for PostgreSQL volume
- Consider adding a load balancer if you scale
- Switch to managed PostgreSQL for larger datasets
