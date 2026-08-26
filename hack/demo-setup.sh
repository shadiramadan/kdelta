#!/usr/bin/env bash
# kdelta demo cluster setup.
#
# Target: a kind cluster on a remote host (SSH_HOST) running
# cert-manager v1.17.2 — intentionally old, so `kdelta versions/changes/impact`
# has real upgrade deltas to analyze. The fixtures in hack/demo/ are chosen so
# the cert-manager changelogs BETWEEN v1.17.2 and current releases contain
# findings that apply to them (default changes, deprecations, removals); see
# the comments in each manifest.
#
# kind binds the API server to 127.0.0.1 on the remote host, so access goes
# through an SSH local port-forward (same port locally) — the kind certificate
# already lists 127.0.0.1 in its SANs, so TLS verification keeps working.
#
# Usage:
#   hack/demo-setup.sh                # tunnel + kubeconfig + install + fixtures + status
#   hack/demo-setup.sh tunnel         # start the SSH port-forward
#   hack/demo-setup.sh kubeconfig     # fetch kubeconfig to hack/.demo-kubeconfig
#   hack/demo-setup.sh install        # install cert-manager v1.17.2 via helm
#   hack/demo-setup.sh fixtures       # apply hack/demo/ manifests
#   hack/demo-setup.sh status         # show cert-manager + fixture state
#   hack/demo-setup.sh clean          # delete the fixtures (leaves cert-manager)
#
# Required env: SSH_HOST — the ssh destination (user@host) running the kind
# cluster. Optional: API_PORT (skips auto-detection), KUBECONFIG_PATH.
set -euo pipefail

SSH_HOST="${SSH_HOST:?set SSH_HOST to the ssh destination running the kind cluster}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
KUBECONFIG_PATH="${KUBECONFIG_PATH:-${SCRIPT_DIR}/.demo-kubeconfig}"
CERT_MANAGER_VERSION="v1.17.2"
export KUBECONFIG="${KUBECONFIG_PATH}"

log() { printf '==> %s\n' "$*"; }

detect_port() {
  if [[ -n "${API_PORT:-}" ]]; then
    echo "${API_PORT}"
    return
  fi
  # kind publishes the API server as 127.0.0.1:<random-port> on the remote host.
  ssh -o BatchMode=yes "${SSH_HOST}" \
    "docker port kind-control-plane 6443/tcp" | head -1 | awk -F: '{print $2}'
}

tunnel() {
  local port
  port="$(detect_port)"
  if nc -z 127.0.0.1 "${port}" 2>/dev/null; then
    log "port-forward to ${SSH_HOST}:${port} already up"
    return
  fi
  log "starting SSH port-forward 127.0.0.1:${port} -> ${SSH_HOST}"
  ssh -f -N -o BatchMode=yes -o ExitOnForwardFailure=yes \
    -L "${port}:127.0.0.1:${port}" "${SSH_HOST}"
}

kubeconfig() {
  log "fetching kind kubeconfig from ${SSH_HOST} -> ${KUBECONFIG_PATH}"
  ssh -o BatchMode=yes "${SSH_HOST}" 'kind get kubeconfig' > "${KUBECONFIG_PATH}"
  chmod 600 "${KUBECONFIG_PATH}"
}

install() {
  log "installing cert-manager ${CERT_MANAGER_VERSION} (idempotent)"
  helm repo add jetstack https://charts.jetstack.io --force-update >/dev/null
  helm upgrade --install cert-manager jetstack/cert-manager \
    --namespace cert-manager --create-namespace \
    --version "${CERT_MANAGER_VERSION}" \
    --set crds.enabled=true \
    --wait --timeout 5m
  kubectl create namespace demo-ns-1 --dry-run=client -o yaml | kubectl apply -f -
  kubectl create namespace demo-ns-2 --dry-run=client -o yaml | kubectl apply -f -
}

fixtures() {
  log "applying demo fixtures from ${SCRIPT_DIR}/demo/"
  kubectl apply -f "${SCRIPT_DIR}/demo/"
  log "waiting for certificates to settle"
  kubectl -n demo-ns-1 wait --for=condition=Ready certificate --all --timeout=120s || true
  kubectl -n demo-ns-2 wait --for=condition=Ready certificate --all --timeout=120s || true
}

status() {
  helm list -n cert-manager
  kubectl get clusterissuers 2>/dev/null || true
  kubectl get issuers,certificates,certificaterequests -n demo-ns-1
  kubectl get issuers,certificates,certificaterequests -n demo-ns-2
  kubectl get secrets -n demo-ns-1 --field-selector type=kubernetes.io/tls
  kubectl get secrets -n demo-ns-2 --field-selector type=kubernetes.io/tls
}

clean() {
  log "deleting demo fixtures"
  kubectl delete -f "${SCRIPT_DIR}/demo/" --ignore-not-found
}

case "${1:-all}" in
  tunnel) tunnel ;;
  kubeconfig) kubeconfig ;;
  install) install ;;
  fixtures) fixtures ;;
  status) status ;;
  clean) clean ;;
  all)
    tunnel
    kubeconfig
    install
    fixtures
    status
    ;;
  *)
    sed -n '2,20p' "${BASH_SOURCE[0]}"
    exit 1
    ;;
esac
