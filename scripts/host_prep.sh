#!/bin/bash
# Host preparation for SeaweedFS.
# Idempotent: sets ulimits, sysctls, firewall rules, and time sync.
set -e

info() {
  echo '[INFO] ->' "$@"
}

warn() {
  echo '[WARN] ->' "$@"
}

fatal() {
  echo '[ERROR] ->' "$@"
  exit 1
}

setup_sudo() {
  SUDO=sudo
  if [ "$(id -u)" -eq 0 ]; then
    SUDO=
  else
    if [ -n "$SUDO_PASS" ]; then
      echo "$SUDO_PASS" | sudo -S true
      echo ""
    fi
  fi
}

# Writes /etc/security/limits.d/seaweed.conf with high nofile values.
setup_limits() {
  info "Configuring ulimits (nofile) for root and seaweed"
  LIMITS_FILE=/etc/security/limits.d/seaweed.conf
  LIMITS_CONTENT='# Managed by seaweed-up host_prep.sh
root soft nofile 1048576
root hard nofile 1048576
seaweed soft nofile 1048576
seaweed hard nofile 1048576
'
  $SUDO mkdir -p /etc/security/limits.d
  if [ -f "$LIMITS_FILE" ] && [ "$($SUDO cat $LIMITS_FILE)" = "$LIMITS_CONTENT" ]; then
    info "limits already up to date"
  else
    echo "$LIMITS_CONTENT" | $SUDO tee "$LIMITS_FILE" >/dev/null
    $SUDO chmod 0644 "$LIMITS_FILE"
    info "wrote $LIMITS_FILE"
  fi
}

# Writes /etc/sysctl.d/99-seaweed.conf and applies sysctl settings.
setup_sysctls() {
  info "Configuring sysctls"
  SYSCTL_FILE=/etc/sysctl.d/99-seaweed.conf
  SYSCTL_CONTENT='# Managed by seaweed-up host_prep.sh
vm.max_map_count=262144
net.core.somaxconn=4096
fs.file-max=2097152
'
  $SUDO mkdir -p /etc/sysctl.d
  if [ -f "$SYSCTL_FILE" ] && [ "$($SUDO cat $SYSCTL_FILE)" = "$SYSCTL_CONTENT" ]; then
    info "sysctl file already up to date"
  else
    echo "$SYSCTL_CONTENT" | $SUDO tee "$SYSCTL_FILE" >/dev/null
    $SUDO chmod 0644 "$SYSCTL_FILE"
    info "wrote $SYSCTL_FILE"
  fi

  if command -v sysctl >/dev/null 2>&1; then
    $SUDO sysctl --system >/dev/null 2>&1 || \
      $SUDO sysctl -p "$SYSCTL_FILE" >/dev/null 2>&1 || \
      warn "sysctl --system returned non-zero; continuing"
  else
    warn "sysctl command not found"
  fi
}

# Opens SeaweedFS ports on the detected firewall (ufw/firewalld/iptables).
setup_firewall() {
  info "Configuring firewall rules"
  PORTS="9333 19333 8080 18080 8888 18888 8333 23646"

  if command -v ufw >/dev/null 2>&1 && $SUDO ufw status 2>/dev/null | grep -qi "Status: active"; then
    info "Detected ufw"
    for p in $PORTS; do
      if $SUDO ufw status | grep -q "^${p}/tcp"; then
        info "ufw rule for ${p}/tcp already present"
      else
        $SUDO ufw allow "${p}/tcp" >/dev/null 2>&1 || warn "ufw allow ${p}/tcp failed"
      fi
    done
    return 0
  fi

  if command -v firewall-cmd >/dev/null 2>&1 && $SUDO firewall-cmd --state >/dev/null 2>&1; then
    info "Detected firewalld"
    for p in $PORTS; do
      if $SUDO firewall-cmd --permanent --query-port="${p}/tcp" >/dev/null 2>&1; then
        info "firewalld rule for ${p}/tcp already present"
      else
        $SUDO firewall-cmd --permanent --add-port="${p}/tcp" >/dev/null 2>&1 || warn "firewall-cmd add ${p}/tcp failed"
      fi
    done
    $SUDO firewall-cmd --reload >/dev/null 2>&1 || warn "firewall-cmd --reload failed"
    return 0
  fi

  if command -v iptables >/dev/null 2>&1; then
    info "Falling back to iptables"
    for p in $PORTS; do
      if $SUDO iptables -C INPUT -p tcp --dport "$p" -j ACCEPT 2>/dev/null; then
        info "iptables rule for tcp/${p} already present"
      else
        $SUDO iptables -I INPUT -p tcp --dport "$p" -j ACCEPT 2>/dev/null || warn "iptables insert ${p} failed"
      fi
    done
    return 0
  fi

  warn "No supported firewall tool found (ufw/firewalld/iptables); skipping firewall config"
}

# Ensures a time sync daemon (chrony or systemd-timesyncd) is enabled and active.
setup_time_sync() {
  info "Configuring time synchronization"
  if ! command -v systemctl >/dev/null 2>&1; then
    warn "systemctl not available; skipping time sync setup"
    return 0
  fi

  if systemctl list-unit-files 2>/dev/null | grep -q '^chrony\(\|d\)\.service'; then
    SVC=chrony
    if systemctl list-unit-files 2>/dev/null | grep -q '^chronyd\.service'; then
      SVC=chronyd
    fi
    info "Ensuring $SVC is enabled and active"
    $SUDO systemctl enable "$SVC" >/dev/null 2>&1 || warn "failed to enable $SVC"
    if systemctl is-active --quiet "$SVC"; then
      info "$SVC already active"
    else
      $SUDO systemctl start "$SVC" >/dev/null 2>&1 || warn "failed to start $SVC"
    fi
    return 0
  fi

  if systemctl list-unit-files 2>/dev/null | grep -q '^systemd-timesyncd\.service'; then
    info "Ensuring systemd-timesyncd is enabled and active"
    $SUDO systemctl enable systemd-timesyncd >/dev/null 2>&1 || warn "failed to enable systemd-timesyncd"
    if systemctl is-active --quiet systemd-timesyncd; then
      info "systemd-timesyncd already active"
    else
      $SUDO systemctl start systemd-timesyncd >/dev/null 2>&1 || warn "failed to start systemd-timesyncd"
    fi
    if command -v timedatectl >/dev/null 2>&1; then
      $SUDO timedatectl set-ntp true >/dev/null 2>&1 || true
    fi
    return 0
  fi

  warn "No chrony or systemd-timesyncd unit found; skipping time sync setup"
}

setup_sudo
setup_limits
setup_sysctls
setup_firewall
setup_time_sync

info "Host preparation complete."
