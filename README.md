# WhatsApp Scheduler

Automated WhatsApp message scheduling with configurable intervals and random delays.

## Features

- ✅ Automated WhatsApp message sending
- ✅ Configurable sending intervals
- ✅ Random delays for natural behavior
- ✅ Time-based scheduling (start and end times)
- ✅ Modern web interface
- ✅ Test message functionality
- ✅ Single task management (one active task at a time)
- ✅ QR code WhatsApp authorization
- ✅ Smart scheduling logic for past start times
- ✅ Real-time UI updates
- ✅ Detailed logging with countdown timers
- ✅ Contact and group chat support

## Requirements

- Go 1.21 or higher
- SQLite3 (automatically handled by Go)

## Installation

### Quick Start

1. Install dependencies:
```bash
go mod tidy
```

2. Build the application:
```bash
# For Windows preinstall gcc from here https://winlibs.com/ and run
.\build-windows.ps1

# For Linux/Mac run
./build.sh
```

3. Run the application

3. The application will automatically open in your browser at: `http://localhost:8080`

## Usage

### Initial Setup

1. On first launch, a QR code will appear in the terminal
2. Scan the QR code using WhatsApp on your phone (Settings → Linked Devices → Link a Device)
3. After successful authorization, the QR code will disappear
4. The web interface will show "Connected" status

### Creating a Scheduled Task

1. Fill out the "Schedule Message" form:
   - **Chat Name**: Enter contact name, group name, or phone number (+1234567890)
   - **Message**: Text message to send
   - **Interval**: Time between sends in minutes
   - **Random Delay**: Additional random delay (0-N minutes, default: 2)
   - **Start Time**: When to begin sending
   - **End Time**: When to stop sending (default: +1 hour from start)

2. Click "Start Task"

**Note**: Only one task can be active at a time. Creating a new task will replace the existing one.

### Smart Scheduling Logic

- If start time is in the past, the system calculates the next send time using the formula: `start_time + (intervals_passed + 1) * interval`
- Detailed logging shows countdown to next message
- Random delays are applied to each message for natural behavior
- Tasks automatically stop when end time is reached

### Test Messages

1. In the "Test Message" section, enter chat name and message
2. Click "Send Test" to verify connectivity
3. Detailed success/error information is displayed

### Task Management

- Current active task is displayed in the "Current Task" card
- Click "Stop" to manually stop the active task
- Tasks automatically stop when the end time is reached
- UI updates in real-time (every 5 seconds when task is active, every 30 seconds when idle)

## Chat Name Formats

The application supports multiple chat name formats:

- **Phone numbers**: `+1234567890`, `1234567890`
- **Contact names**: `John Doe` (exact match)
- **Group names**: `Family Group` (exact match)
- **JID format**: `1234567890@s.whatsapp.net`

## Project Structure

```
whatsapp-scheduler/
├── main.go              # Main application file
├── go.mod               # Go dependencies
├── go.sum               # Dependency hashes
├── ui_templates/           # HTML templates
│   └── index.html       # Main web interface
└── README.md           # Documentation
```

## API Endpoints

- `GET /` - Main web interface
- `GET /qr` - QR code authorization status
- `GET /status` - Detailed WhatsApp client status
- `POST /schedule` - Create new scheduled task
- `POST /replace-task` - Replace existing task
- `GET /tasks` - Get current active task
- `POST /stop/:id` - Stop specific task
- `POST /test` - Send test message

## Configuration

### Default Settings

- **Server Port**: 8080
- **Default Random Delay**: 2 minutes
- **Default End Time**: Start time + 1 hour
- **UI Update Interval**: 5 seconds (with active task), 30 seconds (idle)

### Validation Rules

- Interval must be at least 1 minute
- Random delay cannot exceed interval duration
- Chat name and message cannot be empty
- Start and end times must be valid

## Logging

The application provides detailed logging with emojis for easy reading:

- 🚀 Task creation and startup
- ⏰ Time calculations and scheduling
- ⏳ Countdown to next message
- 🎲 Random delay information
- 📤 Message sending attempts
- ✅ Successful operations
- ❌ Errors and failures
- 🛑 Task stops and completions

## Security & Disclaimer

⚠️ **Important Notice**:
- This application uses the unofficial WhatsApp Web API
- Use at your own risk
- Not recommended for commercial use
- Follow WhatsApp's Terms of Service
- Respect recipient privacy and consent

## Troubleshooting

### Connection Issues

1. Ensure QR code is scanned successfully
2. Check internet connection
3. Restart the application if needed
4. Verify WhatsApp Web is not open in other browsers

### Message Sending Issues

1. Verify chat name format is correct
2. Ensure the chat exists in your WhatsApp
3. Check terminal logs for detailed error messages
4. Try sending a test message first

### Dependency Issues

```bash
# Clean module cache
go clean -modcache

# Reinstall dependencies
go mod tidy

# Force update to latest versions
go get -u ./...
```

### Common Error Messages

- `"чат не найден"` - Chat not found, check chat name
- `"клиент не авторизован"` - WhatsApp not authorized, scan QR code
- `"timeout"` - Network timeout, check connection
- `"неверный интервал"` - Invalid interval, must be ≥ 1 minute

## Development

### Dependencies

Key dependencies:
- `github.com/gin-gonic/gin` - Web framework
- `go.mau.fi/whatsmeow` - WhatsApp Web API client
- `github.com/mattn/go-sqlite3` - SQLite database driver
- `github.com/sirupsen/logrus` - Structured logging
- `github.com/mdp/qrterminal/v3` - QR code terminal display

## License

MIT License

## Support

If you encounter any issues, please create an issue in the project repository with:
- Detailed error description
- Terminal logs
- Steps to reproduce
- Your operating system and Go version