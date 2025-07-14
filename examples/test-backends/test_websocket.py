#!/usr/bin/env python3
"""
WebSocket Backend Test Client

This script tests the WebSocket backend functionality by connecting
and sending various types of messages.
"""

import asyncio
import websockets
import json
import sys
import argparse
from datetime import datetime

async def test_websocket_backend(uri, verbose=False):
    """
    Test the WebSocket backend with various message types
    """
    print(f"Connecting to WebSocket server at {uri}...")
    
    try:
        async with websockets.connect(uri) as websocket:
            print("✅ Connected successfully!")
            
            # Listen for welcome message
            welcome_msg = await websocket.recv()
            welcome_data = json.loads(welcome_msg)
            print(f"📨 Welcome message: {welcome_data['message']}")
            if verbose:
                print(f"   Server info: {welcome_data.get('server_info', {})}")
            
            # Test 1: Ping message
            print("\n🏓 Testing ping message...")
            ping_msg = {
                "type": "ping",
                "message": "Hello from test client!"
            }
            await websocket.send(json.dumps(ping_msg))
            response = await websocket.recv()
            pong_data = json.loads(response)
            
            if pong_data.get('type') == 'pong':
                print("✅ Ping test successful!")
                if verbose:
                    print(f"   Response: {pong_data}")
            else:
                print(f"❌ Ping test failed: {pong_data}")
            
            # Test 2: Echo message
            print("\n🔄 Testing echo message...")
            echo_msg = {
                "type": "echo",
                "message": "This should be echoed back"
            }
            await websocket.send(json.dumps(echo_msg))
            response = await websocket.recv()
            echo_data = json.loads(response)
            
            if echo_data.get('type') == 'echo_response':
                print("✅ Echo test successful!")
                if verbose:
                    print(f"   Original: {echo_msg['message']}")
                    print(f"   Echoed: {echo_data.get('original_message')}")
            else:
                print(f"❌ Echo test failed: {echo_data}")
            
            # Test 3: Stats request
            print("\n📊 Testing stats request...")
            stats_msg = {"type": "stats"}
            await websocket.send(json.dumps(stats_msg))
            response = await websocket.recv()
            stats_data = json.loads(response)
            
            if stats_data.get('type') == 'stats_response':
                print("✅ Stats test successful!")
                stats = stats_data.get('stats', {})
                print(f"   Connected clients: {stats.get('connected_clients')}")
                print(f"   Total messages: {stats.get('total_messages')}")
                print(f"   Uptime: {stats.get('uptime_seconds'):.2f} seconds")
                if verbose:
                    print(f"   Full stats: {stats}")
            else:
                print(f"❌ Stats test failed: {stats_data}")
            
            # Test 4: Broadcast message
            print("\n📢 Testing broadcast message...")
            broadcast_msg = {
                "type": "broadcast",
                "message": "Hello to all connected clients!"
            }
            await websocket.send(json.dumps(broadcast_msg))
            response = await websocket.recv()
            broadcast_data = json.loads(response)
            
            if broadcast_data.get('type') == 'broadcast_confirm':
                print("✅ Broadcast test successful!")
                print(f"   Recipients: {broadcast_data.get('recipients')}")
                if verbose:
                    print(f"   Response: {broadcast_data}")
            else:
                print(f"❌ Broadcast test failed: {broadcast_data}")
            
            # Test 5: Invalid message type
            print("\n❓ Testing invalid message type...")
            invalid_msg = {
                "type": "invalid_type",
                "message": "This should return an error"
            }
            await websocket.send(json.dumps(invalid_msg))
            response = await websocket.recv()
            error_data = json.loads(response)
            
            if error_data.get('type') == 'error':
                print("✅ Error handling test successful!")
                print(f"   Error message: {error_data.get('message')}")
                if verbose:
                    print(f"   Supported types: {error_data.get('supported_types')}")
            else:
                print(f"❌ Error handling test failed: {error_data}")
            
            # Test 6: Invalid JSON
            print("\n🔧 Testing invalid JSON...")
            await websocket.send("invalid json string")
            response = await websocket.recv()
            json_error_data = json.loads(response)
            
            if json_error_data.get('type') == 'error' and 'JSON' in json_error_data.get('message', ''):
                print("✅ JSON error handling test successful!")
                if verbose:
                    print(f"   Error response: {json_error_data}")
            else:
                print(f"❌ JSON error handling test failed: {json_error_data}")
            
            print("\n🎉 All tests completed!")
            
    except (websockets.exceptions.ConnectionClosed, ConnectionRefusedError, OSError) as e:
        print("❌ Connection failed. Make sure the WebSocket backend is running.")
        print("   Start it with: python3 test-backends/websocket_backend.py")
        print(f"   Error: {e}")
        return False
    except Exception as e:
        print(f"❌ Test failed with error: {e}")
        return False
    
    return True

async def interactive_mode(uri):
    """
    Interactive mode for manual testing
    """
    print(f"Connecting to WebSocket server at {uri}...")
    print("Interactive mode - type messages to send, 'quit' to exit")
    print("Example messages:")
    print('  {"type": "ping", "message": "hello"}')
    print('  {"type": "echo", "message": "test"}')
    print('  {"type": "stats"}')
    print('  {"type": "broadcast", "message": "hello everyone"}')
    print()
    
    try:
        async with websockets.connect(uri) as websocket:
            print("✅ Connected! Waiting for welcome message...")
            
            # Listen for welcome message
            welcome_msg = await websocket.recv()
            welcome_data = json.loads(welcome_msg)
            print(f"📨 {welcome_data['message']}")
            
            # Start background task to listen for messages
            async def listen_for_messages():
                try:
                    async for message in websocket:
                        data = json.loads(message)
                        timestamp = datetime.now().strftime("%H:%M:%S")
                        print(f"\n[{timestamp}] 📨 {data.get('type', 'unknown')}: {data.get('message', data)}")
                        print("> ", end="", flush=True)
                except websockets.exceptions.ConnectionClosed:
                    print("\nConnection closed by server.")
                except Exception as e:
                    print(f"\nError receiving message: {e}")
            
            # Start listening task
            listen_task = asyncio.create_task(listen_for_messages())
            
            # Interactive input loop
            while True:
                try:
                    print("> ", end="", flush=True)
                    user_input = input()
                    
                    if user_input.lower() in ['quit', 'exit', 'q']:
                        break
                    
                    if user_input.strip():
                        # Try to parse as JSON, if not, wrap in echo message
                        try:
                            json.loads(user_input)
                            await websocket.send(user_input)
                        except json.JSONDecodeError:
                            # Wrap in echo message
                            echo_msg = {"type": "echo", "message": user_input}
                            await websocket.send(json.dumps(echo_msg))
                            
                except KeyboardInterrupt:
                    break
                except Exception as e:
                    print(f"Error sending message: {e}")
            
            listen_task.cancel()
            print("\nDisconnecting...")
            
    except (websockets.exceptions.ConnectionClosed, ConnectionRefusedError, OSError) as e:
        print("❌ Connection failed. Make sure the WebSocket backend is running.")
        print(f"   Error: {e}")
        return False
    except Exception as e:
        print(f"❌ Connection failed: {e}")
        return False
    
    return True

def main():
    parser = argparse.ArgumentParser(description='WebSocket Backend Test Client')
    parser.add_argument('--host', default='localhost', help='WebSocket server host')
    parser.add_argument('--port', type=int, default=3004, help='WebSocket server port')
    parser.add_argument('--verbose', '-v', action='store_true', help='Verbose output')
    parser.add_argument('--interactive', '-i', action='store_true', help='Interactive mode')
    
    args = parser.parse_args()
    
    uri = f"ws://{args.host}:{args.port}"
    
    if args.interactive:
        success = asyncio.run(interactive_mode(uri))
    else:
        success = asyncio.run(test_websocket_backend(uri, args.verbose))
    
    sys.exit(0 if success else 1)

if __name__ == '__main__':
    main()