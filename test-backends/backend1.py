#!/usr/bin/env python3

import json
import time
from datetime import datetime
from http.server import HTTPServer, BaseHTTPRequestHandler

class BackendHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == '/health':
            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.end_headers()
            response = {
                'status': 'healthy',
                'server': 'backend-1'
            }
            self.wfile.write(json.dumps(response).encode())
            
        elif self.path == '/api/users':
            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.end_headers()
            users = [
                {'id': 1, 'name': 'John Doe', 'server': 'backend-1'},
                {'id': 2, 'name': 'Jane Smith', 'server': 'backend-1'}
            ]
            self.wfile.write(json.dumps(users).encode())
            
        else:
            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.end_headers()
            response = {
                'server': 'Backend Server 1',
                'timestamp': datetime.now().isoformat(),
                'message': 'Hello from Python backend server 1!',
                'port': '3001'
            }
            self.wfile.write(json.dumps(response).encode())
            
        print(f"Request handled by backend 1 - {self.command} {self.path}")
    
    def log_message(self, format, *args):
        # Suppress default logging
        pass

if __name__ == '__main__':
    port = 3001
    server = HTTPServer(('localhost', port), BackendHandler)
    print(f"Backend Server 1 starting on port {port}")
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\nBackend Server 1 stopped")
        server.shutdown()