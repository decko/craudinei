# BotFather Setup Guide

This guide walks you through creating a Telegram bot for Craudinei using [@BotFather](https://t.me/BotFather).

## Step 1: Create the bot

1. Open Telegram and search for **@BotFather**
2. Send `/newbot`
3. BotFather asks for a **display name** — this is the name shown in your contacts and chat list:
   ```
   craudinei
   ```
4. BotFather asks for a **username** — must end in `bot` and be unique:
   ```
   MyCraudineiBot
   ```
5. BotFather confirms and gives you a bot token:
   ```
   123456789:ABCDefGHiJklMNOpQRsTUVwxYZ
   ```

   **Copy this token now.** You will need it for the Craudinei config.

## Step 2: Set the bot description

The description appears at the top of the chat when a user opens your bot.

1. Send `/setdescription` to BotFather
2. Select your bot
3. Enter a short description, for example:
   ```
   Craudinei bridges Claude Code with Telegram. Use /help to see available commands.
   ```

## Step 3: Set the "About" text

This text appears in the bot's profile.

1. Send `/setabouttext` to BotFather
2. Select your bot
3. Enter the about text, for example:
   ```
   Claude Code coding assistant via Telegram. Start a session with /begin, then send prompts and approve tool calls from your phone.
   ```

## Step 4: Set the bot profile picture (optional)

1. Send `/setuserpic` to BotFather
2. Select your bot
3. Send a photo (recommended: 800x400px, 2:1 ratio for best results on mobile and desktop)

## Step 5: Get your Telegram user ID

Before you can use the bot, you need to find your Telegram user ID and add it to the Craudinei config.

1. Open Telegram and search for **@userinfobot**
2. Start a chat and send `/start`
3. The bot replies with your user ID, for example:
   ```
   ID: 123456789
   ```

   **Copy this number.** Add it to `telegram.allowed_users` in your Craudinei config.

## Step 6: Configure Craudinei

Add the bot token and your user ID to the config file:

```yaml
telegram:
  token: "123456789:ABCDefGHiJklMNOpQRsTUVwxYZ"
  allowed_users:
    - 123456789
  auth_passphrase: "${CRAUDINEI_PASSPHRASE}"
```

Set the environment variable with the token:

```bash
export TELEGRAM_BOT_TOKEN=123456789:ABCDefGHiJklMNOpQRsTUVwxYZ
```

## Step 7: Set bot commands (optional)

Craudinei registers its commands automatically via `setMyCommands` on startup. You do not need to configure commands in BotFather — the bot will populate the command list automatically.

However, if you want to set a custom command list for the first screen shown to users (before the bot has been started), you can use `/setcommands` in BotFather:

```
begin - Start a new Claude Code session
stop - Stop the current session
cancel - Interrupt the current task
status - Show session status
auth - Authenticate with passphrase
help - Show all commands
resume - Resume a previous session
sessions - List recent sessions
reload - Reload configuration
```

## Step 8: Do not set a webhook

Craudinei uses **long-polling**, not webhooks. Do not call `/setwebhook` — long-polling is the default and does not require any external exposure.

## Step 9: Keep the bot private

By default, new bots are private. Do not use `/setpublic` or similar BotFather commands to make the bot public. A public bot exposes the interface to anyone on Telegram, which is a security risk.

Only share the bot with people whose user IDs are in `allowed_users`.

## Security considerations

### Bot token

- Store the bot token as an environment variable (`TELEGRAM_BOT_TOKEN`), not inline in the config
- Never commit the token to version control
- Rotate the token if it is compromised: use `/revoke` in BotFather, then update your config

### User ID whitelist

- Add only trusted user IDs to `allowed_users`
- The bot responds to unknown user IDs with a single rate-limited message ("You are not authorized to use this bot.")
- Rate limiting prevents attackers from confirming the bot exists via repeated messages

### Passphrase

- The passphrase (`/auth`) provides a second authentication layer
- Use a memorable but non-obvious phrase
- Failed passphrase attempts are rate-limited: 3 failures in 5 minutes triggers lockout
- The auth message is deleted immediately after verification (or you are prompted to delete it manually if deletion fails)

### Bot privacy

- Keep the bot private
- Do not share the bot username publicly
- Regularly review `allowed_users` and remove IDs that no longer need access

## Troubleshooting

### Bot does not respond

1. Verify the token is correct in your config
2. Verify your user ID is in `allowed_users`
3. Restart Craudinei after config changes
4. Check that `ANTHROPIC_API_KEY` is set (required for Claude Code to run)

### "You are not authorized" message

Your user ID is not in `allowed_users`. Add it to the config and restart.

### Token rejected

Make sure the token format is correct: `123456789:ABCDef...` (bot tokens are always `id:token`). If the token is invalid or revoked, use `/revoke` in BotFather to generate a new one.
