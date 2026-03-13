# Quick Start: Auto-Deploy Setup

Follow these steps in order. This will take ~20 minutes.

## 1️⃣ Create Azure VM (5 min)
- [ ] Go to Azure Portal → Create Virtual Machine
- [ ] Choose `Ubuntu 22.04 LTS`
- [ ] Size: `B1ms` or `B2s`
- [ ] **Inbound rules**: Allow SSH (22) and HTTP (8080)
- [ ] Download SSH key as `vm_key.pem`
- [ ] Note the **public IP address**

## 2️⃣ Setup VM (5 min)
```bash
ssh -i vm_key.pem azureuser@YOUR_VM_IP
sudo apt update && sudo apt upgrade -y
sudo apt install -y docker.io docker-compose-plugin git
sudo usermod -aG docker $USER
newgrp docker
docker --version  # verify it works
```

## 3️⃣ Clone Repository on VM (2 min)
```bash
cd /home/azureuser
git clone https://github.com/YOUR_USERNAME/pdf-to-epub-converter.git
cd pdf-to-epub-converter
cp .env.example .env
```

## 4️⃣ Test Manual Deployment (3 min)
```bash
docker compose up -d --build
curl http://localhost:8080/health
# Should return: {"status":"ok"}
```

## 5️⃣ Setup Auto-Deploy (5 min)

### Generate SSH key pair (on your local machine):
```bash
ssh-keygen -t ed25519 -f github_actions_key -N ""
# Creates: github_actions_key (private) and github_actions_key.pub (public)
```

### Add public key to VM (via SSH):
```bash
ssh -i vm_key.pem azureuser@YOUR_VM_IP
mkdir -p ~/.ssh && chmod 700 ~/.ssh
echo "PASTE_CONTENT_OF_github_actions_key.pub_HERE" >> ~/.ssh/authorized_keys
chmod 600 ~/.ssh/authorized_keys
exit
```

### Add secrets to GitHub:
1. Go to your repo → **Settings → Secrets and variables → Actions**
2. Click **New repository secret** and add:

| Name | Value |
|------|-------|
| `VM_HOST` | Your VM's public IP (e.g., `20.123.45.67`) |
| `VM_USER` | `azureuser` |
| `VM_SSH_KEY` | Content of `github_actions_key` (the **private** key) |

## 6️⃣ Test Auto-Deploy (2 min)

Make a small change and push:
```bash
git add .
git commit -m "Test deployment"
git push origin main
```

Watch it deploy:
- Go to GitHub → **Actions** tab
- Click the workflow run
- Should complete in ~2 minutes
- Check your VM: `curl http://YOUR_VM_IP:8080/health`

---

## ✅ Done!

Now every time you push to `main`:
- ✅ GitHub Actions runs
- ✅ Connects to your VM
- ✅ Pulls latest code
- ✅ Rebuilds containers
- ✅ Restarts the app
- **Total time**: ~2 minutes

---

## 📚 Full Documentation

See `DEPLOYMENT.md` for detailed instructions and troubleshooting.
