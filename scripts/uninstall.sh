#!/bin/bash
set -e

info() {
  echo '[INFO] ->' "$@"
}

fatal() {
  echo '[ERROR] ->' "$@"
  exit 1
}

verify_system() {
  if ! [ -d /run/systemd ]; then
    fatal "Can not find systemd to use as a process supervisor"
  fi
}

setup_env() {
  SUDO=sudo
  if [ "$(id -u)" -eq 0 ]; then
    SUDO=
  else
    if [ ! -z "$SUDO_PASS" ]; then
      echo $SUDO_PASS | sudo -S true
      echo ""
    fi
  fi

  BIN_DIR=/usr/local/bin
  BINARY=weed

  COMPONENT_INSTANCE={{.ComponentInstance}}
  COMPONENT={{.Component}}

  SEAWEED_COMPONENT_INSTANCE_DATA_DIR=/opt/seaweed/${COMPONENT_INSTANCE}
  SEAWEED_COMPONENT_INSTANCE_CONFIG_DIR=/etc/seaweed/${COMPONENT_INSTANCE}.d
  SEAWEED_COMPONENT_INSTANCE_SERVICE_FILE=/etc/systemd/system/seaweed_${COMPONENT_INSTANCE}.service

}

stop_and_disable_service() {
  info "Stopping and disabling systemd service"
  $SUDO systemctl stop $COMPONENT_INSTANCE
  $SUDO systemctl disable $COMPONENT_INSTANCE
  $SUDO systemctl daemon-reload
}

clean_up() {
  info "Removing installation"
  $SUDO rm -rf $SEAWEED_COMPONENT_INSTANCE_CONFIG_DIR
  $SUDO rm -rf $SEAWEED_COMPONENT_INSTANCE_DATA_DIR
  $SUDO rm -rf $SEAWEED_COMPONENT_INSTANCE_SERVICE_FILE
  $SUDO rm -rf $BIN_DIR/$BINARY
}

verify_system
setup_env
stop_and_disable_service
clean_up
