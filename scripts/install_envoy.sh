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
    fatal "Can not find systemd to use as a process supervisor for seaweed_${PRODUCT}"
  fi
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

  COMPONENT_INSTANCE={{.ComponentInstance}}
  COMPONENT={{.Component}}
  CONFIG_DIR={{.ConfigDir}}
  DATA_DIR={{.DataDir}}

  SEAWEED_COMPONENT_INSTANCE_DATA_DIR=${DATA_DIR}/${COMPONENT_INSTANCE}
  SEAWEED_COMPONENT_INSTANCE_CONFIG_DIR=${CONFIG_DIR}/${COMPONENT_INSTANCE}.d
  SEAWEED_COMPONENT_INSTANCE_SERVICE_FILE=/etc/systemd/system/seaweed_${COMPONENT_INSTANCE}.service

  BIN_DIR=/usr/local/bin
  BINARY=envoy

  PRE_INSTALL_HASHES=$(get_installed_hashes)

  TMP_DIR={{.TmpDir}}
  SKIP_ENABLE={{.SkipEnable}}
  SKIP_START={{.SkipStart}}
  SEAWEED_VERSION={{.Version}}

  cd $TMP_DIR
}

# --- set arch and suffix, fatal if architecture not supported ---
setup_verify_arch() {
  if [ -z "$ARCH" ]; then
    ARCH=$(uname -m)
  fi
  case $ARCH in
  amd64)
    SUFFIX=amd64
    ;;
  x86_64)
    SUFFIX=amd64
    ;;
  arm64)
    SUFFIX=arm64
    ;;
  aarch64)
    SUFFIX=arm64
    ;;
  arm*)
    SUFFIX=arm
    ;;
  *)
    fatal "Unsupported architecture $ARCH"
    ;;
  esac
}

# --- get hashes of the current seaweed bin and service files
get_installed_hashes() {
  setup_sudo
  echo "found binary ${BIN_DIR}/${BINARY}"
  $SUDO sha256sum ${BIN_DIR}/${BINARY} ${SEAWEED_COMPONENT_INSTANCE_CONFIG_DIR}/* ${SEAWEED_COMPONENT_INSTANCE_SERVICE_FILE} 2>&1 || true
}

has_yum() {
  [ -n "$(command -v yum)" ]
}

has_apt_get() {
  [ -n "$(command -v apt-get)" ]
}

install_dependencies() {
  if [ ! -x "${TMP_DIR}/seaweed_${COMPONENT_INSTANCE}" ]; then
    if ! [ -x "$(command -v tar)" ] || ! [ -x "$(command -v curl)" ]; then
      if $(has_apt_get); then
        $SUDO apt-get install -y curl tar
      elif $(has_yum); then
        $SUDO yum install -y curl tar
      else
        fatal "Could not find apt-get or yum. Cannot install dependencies on this OS"
        exit 1
      fi
    fi
  fi
}

download_and_install() {
  if [ -x "${BIN_DIR}/${BINARY}" ] && [ "$(${BIN_DIR}/${BINARY} --version | grep "\S" | cut -d'/' -f6)" = "${SEAWEED_VERSION}" ]; then
    info "Envoy binary already installed in ${BIN_DIR}, skipping downloading and installing binary"
  else
    OS="linux"
    assetFileName="${BINARY}-${SEAWEED_VERSION}-${OS}-${ARCH}"
    sourceUrl="https://github.com/envoyproxy/envoy/releases/download/v${SEAWEED_VERSION}/${assetFileName}"
    info "Downloading ${sourceUrl} to ${BIN_DIR}/${BINARY}"
    $SUDO curl -o "${TMP_DIR}/${BINARY}" -fL "${sourceUrl}"
    $SUDO chmod 755 ${TMP_DIR}/${BINARY}
    $SUDO mv ${TMP_DIR}/${BINARY} ${BIN_DIR}/${BINARY}
  fi
}

create_user_and_config() {
  $SUDO mkdir --parents ${SEAWEED_COMPONENT_INSTANCE_DATA_DIR}
  $SUDO mkdir --parents ${SEAWEED_COMPONENT_INSTANCE_CONFIG_DIR}

    if [ "$(ls -A ${TMP_DIR}/config/)" ]; then
      info "Copying configuration files"
      $SUDO cp ${TMP_DIR}/config/* ${SEAWEED_COMPONENT_INSTANCE_CONFIG_DIR}
    fi
}

# --- write systemd service file ---
create_systemd_service_file() {
  info "Adding systemd service file ${SEAWEED_COMPONENT_INSTANCE_SERVICE_FILE}"
  $SUDO tee ${SEAWEED_COMPONENT_INSTANCE_SERVICE_FILE} >/dev/null <<EOF
[Unit]
Description=Seaweed${COMPONENT_INSTANCE}
Documentation=https://github.com/seaweedfs/seaweedfs/wiki
Wants=network-online.target
After=network-online.target

[Service]
WorkingDirectory=${SEAWEED_COMPONENT_INSTANCE_DATA_DIR}
ExecStart=${BIN_DIR}/${BINARY} --log-path envoy.log --config-path ${SEAWEED_COMPONENT_INSTANCE_CONFIG_DIR}/envoy.yaml
ExecReload=/bin/kill -s HUP \$MAINPID
KillMode=process
KillSignal=SIGINT
LimitNOFILE=infinity
LimitNPROC=infinity
Restart=on-failure
RestartSec=2
StartLimitBurst=3
StartLimitIntervalSec=10
TasksMax=infinity

[Install]
WantedBy=multi-user.target
EOF
}

# --- startup systemd service ---
systemd_enable_and_start() {
  [ "${SKIP_ENABLE}" = true ] && return

  info "Enabling systemd service"
  $SUDO systemctl enable ${SEAWEED_COMPONENT_INSTANCE_SERVICE_FILE} >/dev/null
  $SUDO systemctl daemon-reload >/dev/null

  [ "${SKIP_START}" = true ] && return

  POST_INSTALL_HASHES=$(get_installed_hashes)
  echo "before ${PRE_INSTALL_HASHES} => ${POST_INSTALL_HASHES}"
  if [ "${PRE_INSTALL_HASHES}" = "${POST_INSTALL_HASHES}" ]; then
    info "No change detected so skipping service start"
    return
  fi

  info "Starting systemd service"
  $SUDO systemctl restart seaweed_${COMPONENT_INSTANCE}

  return 0
}

setup_env
setup_verify_arch
verify_system
install_dependencies
create_user_and_config
download_and_install
create_systemd_service_file
systemd_enable_and_start
