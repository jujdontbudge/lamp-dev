# Lamp

Lamp is open-source software anyone installs on their own machines to create fast, isolated sandboxes on demand for AI agents, harnesses and CI.

## Sandboxes

**Sandbox**:
An isolated computer that Lamp creates on demand, equipped and sized for one piece of work.
_Avoid_: container, VM, environment, workspace

**Template**:
A prepared starting point that sandboxes are created from.
_Avoid_: image, base, snapshot

**Fork**:
A new sandbox that starts as an exact copy of a running one, memory included.
_Avoid_: clone, branch, copy

**Recipe**:
A named, saved combination of a template, harness, kits, devices, resources, network setup and connections that a caller asks for to get a ready sandbox.
_Avoid_: profile, spec, blueprint, preset

**Device**:
Virtual hardware attached to a sandbox only when asked for, such as a display, audio, keyboard and mouse, or a GPU.
_Avoid_: peripheral, capability

**Kit**:
Software plus its saved state that can be added to a sandbox, such as a logged-in browser, a password manager or a language toolchain.
_Avoid_: add-on, capability, extension, plugin

**Connection**:
An approved link from a sandbox to something outside it: a signed-in MCP server, an API used by a CLI tool, or a service on the owner's own machines.
_Avoid_: integration, bridge, pass-through

## Machines and access

**Host**:
A machine that Lamp is installed on and that runs sandboxes.
_Avoid_: device, node, server

**Fleet**:
All the hosts behind one Lamp address, sharing one set of keys. A single host is a fleet of one.
_Avoid_: cluster, pool, install

**Caller**:
Anyone or anything that creates or controls sandboxes through Lamp's API, CLI or MCP: a person, an agent, or a CI system.
_Avoid_: client, user

**Relay**:
An open-source service, also run as a hosted offering by the Lamp project, that lets callers reach a host that isn't publicly reachable without the host's owner setting up any networking.
_Avoid_: tunnel, proxy

**Eilmer account**:
The one account a person uses across every Eilmer product, Lamp included.
_Avoid_: Lamp account, Clerk account, login

**Plugin**:
A separately run program, which anyone can write, that adds a new option to Lamp itself, such as a VPN type or a CI system. A plugin extends Lamp; a kit goes into a sandbox.
_Avoid_: extension, add-on, kit

## Agents

**Harness**:
The agent program that runs a model and does the work, such as Claude Code, Codex or OpenCode.
_Avoid_: agent app, CLI

**Session**:
One harness's running piece of work inside a sandbox, which a person can watch, join or talk to.
_Avoid_: thread, conversation, chat

**Console**:
Any app a person uses to watch and talk to sessions, such as T3 Code, Herdr or an SSH terminal. Lamp works with whichever console the user sets up.
_Avoid_: workbench, front end, client

**Sub-agent**:
A session started by another session. It runs in its own sandbox or shares its parent's, as its starter chooses.
_Avoid_: child agent, helper
