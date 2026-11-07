# Shellcheck-friendly library: redact credentials before reviewer transport.
# shellcheck shell=bash

reviewer_redact_sensitive_text() {
  if command -v perl >/dev/null 2>&1; then
    perl -0777 -pe '
      s/-----BEGIN (?:RSA |OPENSSH |DSA |EC |)?PRIVATE KEY-----.*?-----END (?:RSA |OPENSSH |DSA |EC |)?PRIVATE KEY-----/[REDACTED]/gs;
      s/(?:Authorization:?\s*)?Bearer\s+[A-Za-z0-9._~+\/=-]+/[REDACTED]/ig;
      s/\b[A-Z0-9_]*(?:PASSWORD|PASSWD|TOKEN|SECRET|API_?KEY|ACCESS_?KEY|CREDENTIAL|PRIVATE_?KEY|AUTHORIZATION|AUTH|COOKIE)[A-Z0-9_]*\s*=\s*(?:"[^"]*"|\x27[^\x27]*\x27|[^\s]+)/[REDACTED]/ig;
      s/\b(?:--|-)?[A-Za-z0-9_.-]*(?:password|passwd|token|secret|api[-_]?key|access[-_]?key|credential|private[-_]?key|authorization|auth|cookie)[A-Za-z0-9_.-]*(?:=|\s+)(?:"[^"]*"|\x27[^\x27]*\x27|[^\s]+)/[REDACTED]/ig;
      s/sk-[A-Za-z0-9_-]{10,}/[REDACTED]/g;
      s/gh[pousr]_[A-Za-z0-9_]{10,}/[REDACTED]/g;
      s/github_pat_[A-Za-z0-9_]{20,}/[REDACTED]/g;
      s/glpat-[A-Za-z0-9_-]{10,}/[REDACTED]/g;
      s/xox[baprs]-[A-Za-z0-9-]{10,}/[REDACTED]/g;
      s/AKIA[0-9A-Z]{16}/[REDACTED]/g;
      s/ASIA[0-9A-Z]{16}/[REDACTED]/g;
      s/AIza[0-9A-Za-z_-]{20,}/[REDACTED]/g;
      s/SG\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}/[REDACTED]/g;
      s/\b[rs]k_(?:live|test)_[A-Za-z0-9]{10,}/[REDACTED]/g;
    '
  else
    sed -E 's/(Bearer[[:space:]]+)[^[:space:]]+/\1[REDACTED]/Ig; s/([Pp]assword|[Pp]asswd|[Tt]oken|[Ss]ecret|[Aa][Pp][Ii][-_]?[Kk]ey|[Cc]redential)[^[:space:]]*/[REDACTED]/g'
  fi
}

redact_sensitive_text() {
  reviewer_redact_sensitive_text "$@"
}
