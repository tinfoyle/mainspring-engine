{
  admin off
  auto_https off
}

:8080 {
  handle /health/ready {
    respond "ready" 200
  }
  root * /srv
  try_files {path} /index.html
  header {
    X-Content-Type-Options nosniff
    Referrer-Policy same-origin
    Permissions-Policy "camera=(), geolocation=(), microphone=()"
    Content-Security-Policy "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"
  }
  file_server
}
