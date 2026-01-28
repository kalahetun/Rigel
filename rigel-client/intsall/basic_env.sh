#!/usr/bin/env bash
set -e

GO_VERSION="1.21.3"
GO_TAR="go${GO_VERSION}.linux-amd64.tar.gz"
GO_URL="https://dl.google.com/go/${GO_TAR}"

echo "==> Update system"
sudo apt update && sudo apt upgrade -y

echo "==> Install basic tools"
sudo apt install -y git vim wget build-essential ca-certificates

# ---------- tier-1 essential tools ----------
echo "==> Install tier-1 essential tools (curl / htop / tmux)"
sudo apt install -y curl htop tmux

echo "==> Verify tier-1 installation"
curl --version | head -n 1
htop --version
tmux -V
# -------------------------------------------

echo "==> Install Go ${GO_VERSION}"
if [ -d "/usr/local/go" ]; then
    echo "Found existing /usr/local/go, backing up to /usr/local/go.bak"
    sudo mv /usr/local/go /usr/local/go.bak
fi

wget -q ${GO_URL} -O /tmp/${GO_TAR}
sudo tar -C /usr/local -xzf /tmp/${GO_TAR}
rm -f /tmp/${GO_TAR}

echo "==> Configure Go environment"
BASHRC="$HOME/.bashrc"

if ! grep -q "GOROOT=/usr/local/go" "$BASHRC"; then
cat << EOF >> "$BASHRC"

# Go ${GO_VERSION} environment
export GOROOT=/usr/local/go
export GOPATH=\$HOME/go
export PATH=\$PATH:\$GOROOT/bin:\$GOPATH/bin
export GOPROXY="https://proxy.golang.org,direct"
EOF
fi

mkdir -p "$HOME/go"

# ===== immediate effect =====
export GOROOT=/usr/local/go
export GOPATH=$HOME/go
export PATH=$PATH:$GOROOT/bin:$GOPATH/bin

echo "==> Verify installation"
git --version
go version

echo "==> Done"