# Surikiti Reverse Proxy

Reverse proxy server yang dibangun menggunakan gnet dengan konfigurasi upstream yang dapat dibaca dari file TOML menggunakan Viper.

## Fitur

- **High Performance**: Menggunakan gnet untuk performa tinggi
- **Load Balancing**: Mendukung berbagai algoritma load balancing:
  - Round Robin
  - Weighted Round Robin
  - Least Connections
- **Health Checks**: Monitoring kesehatan upstream servers secara otomatis
- **Konfigurasi Fleksibel**: Menggunakan file TOML untuk konfigurasi
- **Logging**: Logging terstruktur dengan rotasi file
- **Graceful Shutdown**: Shutdown yang aman

## Instalasi

1. Clone repository ini
2. Install dependencies:
   ```bash
   go mod tidy
   ```

## Konfigurasi

Edit file `config.toml` untuk mengatur server dan upstream:

```toml
[server]
port = 8080
host = "0.0.0.0"

# Upstream servers configuration
[[upstreams]]
name = "backend1"
url = "http://localhost:3001"
weight = 1
health_check = "/health"

[[upstreams]]
name = "backend2"
url = "http://localhost:3002"
weight = 1
health_check = "/health"

[load_balancer]
method = "round_robin"  # round_robin, weighted_round_robin, least_connections
timeout = "30s"
max_retries = 3

[logging]
level = "info"
file = "proxy.log"
```

### Konfigurasi Server

- `port`: Port untuk reverse proxy server
- `host`: Host address untuk binding

### Konfigurasi Upstream

- `name`: Nama identifier untuk upstream server
- `url`: URL lengkap upstream server
- `weight`: Bobot untuk weighted round robin (default: 1)
- `health_check`: Endpoint untuk health check

### Konfigurasi Load Balancer

- `method`: Algoritma load balancing
  - `round_robin`: Distribusi bergiliran
  - `weighted_round_robin`: Berdasarkan bobot
  - `least_connections`: Server dengan koneksi paling sedikit
- `timeout`: Timeout untuk request ke upstream
- `max_retries`: Maksimal retry jika request gagal

### Konfigurasi Logging

- `level`: Level logging (debug, info, warn, error)
- `file`: File untuk menyimpan log

## Menjalankan Server

```bash
# Menggunakan konfigurasi default (config.toml)
go run main.go

# Menggunakan file konfigurasi custom
go run main.go -config /path/to/custom-config.toml
```

## Build Binary

```bash
go build -o surikiti main.go
./surikiti -config config.toml
```

## Testing

Untuk testing, Anda bisa menjalankan backend server Python yang sudah disediakan:

```bash
# Menjalankan semua backend servers sekaligus
./start-backends.sh

# Atau menjalankan secara manual di terminal terpisah:
# Terminal 1 - Backend server 1
python3 test-backends/backend1.py

# Terminal 2 - Backend server 2
python3 test-backends/backend2.py

# Terminal 3 - Backend server 3
python3 test-backends/backend3.py

# Terminal 4 - Reverse proxy
go run main.go
```

Kemudian test dengan curl:

```bash
curl http://localhost:8080
```

## Monitoring

- Log file akan dibuat sesuai konfigurasi (`proxy.log` secara default)
- Health check berjalan setiap 30 detik
- Metrics koneksi dan status upstream tersedia di log

## Struktur Project

```
.
├── main.go              # Entry point aplikasi
├── config.toml          # File konfigurasi
├── start-backends.sh    # Script untuk menjalankan backend servers
├── config/
│   └── config.go        # Struktur dan loader konfigurasi
├── loadbalancer/
│   └── loadbalancer.go  # Implementasi load balancer
├── proxy/
│   └── proxy.go         # HTTP proxy handler
└── test-backends/
    ├── backend1.py      # Backend server Python 1
    ├── backend2.py      # Backend server Python 2
    └── backend3.py      # Backend server Python 3
```

## Dependencies

- `github.com/panjf2000/gnet/v2`: High-performance networking framework
- `github.com/spf13/viper`: Configuration management
- `go.uber.org/zap`: Structured logging
- `gopkg.in/natefinch/lumberjack.v2`: Log rotation

## License

MIT License