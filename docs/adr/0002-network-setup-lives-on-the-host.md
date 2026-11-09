# A sandbox's network setup lives on the host

The host attaches every part of a sandbox's network: VPNs, proxies and exit IPs, private-network membership, inbound URLs, allow and deny lists, and links to other sandboxes. Their keys stay on the host. An agent with root inside the sandbox can't read the VPN credentials or remove the rules. Configuring networking inside the sandbox would be simpler, but any agent could undo it.
