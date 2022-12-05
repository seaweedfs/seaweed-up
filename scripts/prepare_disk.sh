#!/bin/bash
set -e

info() {
  echo '[INFO] ->' "$@"
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
    if [ ! -z "$SUDO_PASS" ]; then
      echo "$SUDO_PASS" | sudo -S true
      echo ""
    fi
  fi
}

setup_env() {
  setup_sudo

  MOUNT_POINT={{.MountPoint}}
  DEVICE_PATH={{.DevicePath}}
}

setup_mount() {

  info "Setup Mount Point"
  $SUDO mkdir -p -m 755 ${MOUNT_POINT}
  info "add ${DEVICE_PATH} ${MOUNT_POINT} to fstab"
  echo "${DEVICE_PATH} ${MOUNT_POINT} ext4 noatime 0 2" | $SUDO tee -a /etc/fstab
  info "mount ${DEVICE_PATH} ${MOUNT_POINT}"
  $SUDO mount ${DEVICE_PATH} ${MOUNT_POINT}

  return 0
}

setup_env
setup_mount
