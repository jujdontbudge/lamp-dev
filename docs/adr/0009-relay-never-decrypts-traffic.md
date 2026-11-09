# The relay routes traffic without decrypting it

The relay signs people in, links hosts to accounts and hands out one-time credentials. Traffic between callers and hosts stays end-to-end encrypted, and the relay only forwards it, the way Home Assistant's remote access works. The Ozi relay used Cloudflare Tunnels, which we considered but rejected for two reasons: Cloudflare decrypts traffic at its edge, and self-hosting a relay would then require a Cloudflare account. The relay holds no model credentials or sandbox data.
