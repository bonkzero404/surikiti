#!/usr/bin/env python3
"""
WebSocket Backend Server for Surikiti Proxy Testing

This server provides WebSocket endpoints for testing the WebSocket proxy functionality.
It demonstrates real-time bidirectional communication capabilities.
"""

import asyncio
import websockets
import json
import logging
import time
from datetime import datetime
import argparse
import signal
import sys

# Configure logging
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(name)s - %(levelname)s - %(message)s'
)
logger = logging.getLogger('websocket_backend')

class WebSocketBackend:
    def __init__(self, host='localhost', port=3004):
        self.host = host
        self.port = port
        self.clients = set()
        self.message_count = 0
        self.start_time = time.time()
        
    async def register_client(self, websocket):
        """Register a new WebSocket client"""
        self.clients.add(websocket)
        client_info = f"{websocket.remote_address[0]}:{websocket.remote_address[1]}"
        logger.info(f"Client connected: {client_info} (Total: {len(self.clients)})")
        
        # Send welcome message
        welcome_msg = {
            "type": "welcome",
            "message": "Connected to WebSocket Backend",
            "server_info": {
                "host": self.host,
                "port": self.port,
                "uptime": time.time() - self.start_time,
                "total_clients": len(self.clients)
            },
            "timestamp": datetime.now().isoformat()
        }
        await websocket.send(json.dumps(welcome_msg))
        
    async def unregister_client(self, websocket):
        """Unregister a WebSocket client"""
        self.clients.discard(websocket)
        logger.info(f"Client disconnected (Total: {len(self.clients)})")
        
    async def broadcast_message(self, message, sender=None):
        """Broadcast message to all connected clients"""
        if self.clients:
            broadcast_data = {
                "type": "broadcast",
                "message": message,
                "sender": str(sender.remote_address) if sender else "server",
                "timestamp": datetime.now().isoformat(),
                "total_clients": len(self.clients)
            }
            
            # Send to all clients except sender
            recipients = self.clients - {sender} if sender else self.clients
            if recipients:
                await asyncio.gather(
                    *[client.send(json.dumps(broadcast_data)) for client in recipients],
                    return_exceptions=True
                )
                
    async def handle_client_message(self, websocket, message):
        """Handle incoming message from client"""
        try:
            data = json.loads(message)
            msg_type = data.get('type', 'unknown')
            
            self.message_count += 1
            logger.info(f"Received {msg_type} message from {websocket.remote_address}")
            
            if msg_type == 'ping':
                # Respond to ping with pong
                pong_response = {
                    "type": "pong",
                    "original_message": data,
                    "server_time": datetime.now().isoformat(),
                    "message_count": self.message_count
                }
                await websocket.send(json.dumps(pong_response))
                
            elif msg_type == 'echo':
                # Echo the message back
                echo_response = {
                    "type": "echo_response",
                    "original_message": data.get('message', ''),
                    "echoed_at": datetime.now().isoformat(),
                    "message_count": self.message_count
                }
                await websocket.send(json.dumps(echo_response))
                
            elif msg_type == 'broadcast':
                # Broadcast message to all other clients
                await self.broadcast_message(data.get('message', ''), sender=websocket)
                
                # Confirm broadcast to sender
                confirm_response = {
                    "type": "broadcast_confirm",
                    "message": "Message broadcasted successfully",
                    "recipients": len(self.clients) - 1,
                    "timestamp": datetime.now().isoformat()
                }
                await websocket.send(json.dumps(confirm_response))
                
            elif msg_type == 'stats':
                # Send server statistics
                stats_response = {
                    "type": "stats_response",
                    "stats": {
                        "connected_clients": len(self.clients),
                        "total_messages": self.message_count,
                        "uptime_seconds": time.time() - self.start_time,
                        "server_host": self.host,
                        "server_port": self.port
                    },
                    "timestamp": datetime.now().isoformat()
                }
                await websocket.send(json.dumps(stats_response))
                
            else:
                # Unknown message type
                error_response = {
                    "type": "error",
                    "message": f"Unknown message type: {msg_type}",
                    "supported_types": ["ping", "echo", "broadcast", "stats"],
                    "timestamp": datetime.now().isoformat()
                }
                await websocket.send(json.dumps(error_response))
                
        except json.JSONDecodeError:
            # Handle invalid JSON
            error_response = {
                "type": "error",
                "message": "Invalid JSON format",
                "received": message[:100],  # First 100 chars
                "timestamp": datetime.now().isoformat()
            }
            await websocket.send(json.dumps(error_response))
            
        except Exception as e:
            logger.error(f"Error handling message: {e}")
            error_response = {
                "type": "error",
                "message": "Internal server error",
                "timestamp": datetime.now().isoformat()
            }
            await websocket.send(json.dumps(error_response))
            
    async def handle_client(self, websocket):
        """Handle WebSocket client connection"""
        await self.register_client(websocket)
        
        try:
            async for message in websocket:
                await self.handle_client_message(websocket, message)
        except websockets.exceptions.ConnectionClosed:
            logger.info("Client connection closed normally")
        except Exception as e:
            logger.error(f"Error in client handler: {e}")
        finally:
            await self.unregister_client(websocket)
            
    async def periodic_broadcast(self):
        """Send periodic messages to all clients"""
        while True:
            await asyncio.sleep(30)  # Every 30 seconds
            if self.clients:
                periodic_msg = {
                    "type": "periodic_update",
                    "message": "Server heartbeat",
                    "server_stats": {
                        "connected_clients": len(self.clients),
                        "total_messages": self.message_count,
                        "uptime": time.time() - self.start_time
                    },
                    "timestamp": datetime.now().isoformat()
                }
                
                await asyncio.gather(
                    *[client.send(json.dumps(periodic_msg)) for client in self.clients],
                    return_exceptions=True
                )
                logger.info(f"Sent periodic update to {len(self.clients)} clients")
                
    async def start_server(self):
        """Start the WebSocket server"""
        logger.info(f"Starting WebSocket server on {self.host}:{self.port}")
        
        # Start periodic broadcast task
        asyncio.create_task(self.periodic_broadcast())
        
        # Start WebSocket server
        server = await websockets.serve(
            lambda ws: self.handle_client(ws),
            self.host,
            self.port,
            ping_interval=20,
            ping_timeout=10
        )
        
        logger.info(f"WebSocket server started on ws://{self.host}:{self.port}")
        logger.info("Supported message types: ping, echo, broadcast, stats")
        logger.info("Example usage:")
        logger.info(f"  wscat -c ws://{self.host}:{self.port}")
        logger.info("  Send: {\"type\": \"ping\", \"message\": \"hello\"}")
        
        return server

def signal_handler(signum, frame):
    """Handle shutdown signals"""
    logger.info(f"Received signal {signum}, shutting down...")
    sys.exit(0)

async def main():
    parser = argparse.ArgumentParser(description='WebSocket Backend Server')
    parser.add_argument('--host', default='localhost', help='Host to bind to')
    parser.add_argument('--port', type=int, default=3004, help='Port to bind to')
    parser.add_argument('--debug', action='store_true', help='Enable debug logging')
    
    args = parser.parse_args()
    
    if args.debug:
        logging.getLogger().setLevel(logging.DEBUG)
        
    # Setup signal handlers
    signal.signal(signal.SIGINT, signal_handler)
    signal.signal(signal.SIGTERM, signal_handler)
    
    # Create and start server
    backend = WebSocketBackend(args.host, args.port)
    server = await backend.start_server()
    
    # Keep server running
    try:
        await server.wait_closed()
    except KeyboardInterrupt:
        logger.info("Server stopped by user")
    finally:
        server.close()
        await server.wait_closed()
        logger.info("WebSocket server shutdown complete")

if __name__ == '__main__':
    try:
        asyncio.run(main())
    except KeyboardInterrupt:
        logger.info("Server interrupted by user")
    except Exception as e:
        logger.error(f"Server error: {e}")
        sys.exit(1)