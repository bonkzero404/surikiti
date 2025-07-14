#!/bin/bash

# Generate self-signed certificates for HTTP/2 and HTTP/3 testing

echo "🔐 Generating self-signed certificates for HTTP/2 and HTTP/3..."

# Create certs directory if it doesn't exist
mkdir -p certs

# Generate private key
echo "📝 Generating private key..."
openssl genrsa -out certs/server.key 2048

# Generate certificate signing request
echo "📋 Generating certificate signing request..."
openssl req -new -key certs/server.key -out certs/server.csr -subj "/C=US/ST=CA/L=San Francisco/O=Surikiti/OU=Development/CN=localhost"

# Generate self-signed certificate
echo "🏆 Generating self-signed certificate..."
openssl x509 -req -days 365 -in certs/server.csr -signkey certs/server.key -out certs/server.crt -extensions v3_req -extfile <(
cat <<EOF
[v3_req]
basicConstraints = CA:FALSE
keyUsage = nonRepudiation, digitalSignature, keyEncipherment
subjectAltName = @alt_names

[alt_names]
DNS.1 = localhost
DNS.2 = *.localhost
IP.1 = 127.0.0.1
IP.2 = ::1
EOF
)

# Clean up CSR file
rm certs/server.csr

# Set appropriate permissions
chmod 600 certs/server.key
chmod 644 certs/server.crt

echo "✅ Certificates generated successfully!"
echo "📁 Certificate files:"
echo "   - Private key: certs/server.key"
echo "   - Certificate: certs/server.crt"
echo ""
echo "🔧 To enable HTTPS/HTTP2/HTTP3, update your config.toml:"
echo "   tls_cert_file = \"certs/server.crt\""
echo "   tls_key_file = \"certs/server.key\""
echo ""
echo "⚠️  Note: This is a self-signed certificate for development only."
echo "   Browsers will show security warnings."
echo "   For production, use certificates from a trusted CA."