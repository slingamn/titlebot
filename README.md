titlebot
========

titlebot is a simple IRC bot that:

1. Automatically extracts and sends the titles of webpages posted in your channel
1. Automatically downloads and reads tweets linked in your channel
1. Demonstrates some of the IRCv3 support provided by [ergochat/irc-go](https://github.com/ergochat/irc-go)

It is configured using environment variables:

```bash
# required:
export TITLEBOT_NICK=titlebot
export TITLEBOT_SERVER="testnet.oragono.io:6697"
export TITLEBOT_CHANNELS="#chat"

# optional:
# this is the account of the bot's owner, to be checked against account-tag:
export TITLEBOT_OWNER_ACCOUNT="shivaram"
# SASL credentials:
#export TITLEBOT_SASL_LOGIN=titlebot
#export TITLEBOT_SASL_PASSWORD=lLRpGzfro1sIFwZZ4kNdpA
# Twitter API bearer token, v2-capable:
export TITLEBOT_TWITTER_BEARER_TOKEN=AAAAAAAAAAAAAAAAAAAAA1AqIi4cLk9SEH6YadRSwwhul6X_a_C6i63ZM3mKFVwoJXxJji1KN0VXCN_rajcX8k4rX4Q-GIbVJ1NVfCA7208
# quit message:
export TITLEBOT_VERSION="titlebot-v0.0.1-alpha-dont-deploy"
```
