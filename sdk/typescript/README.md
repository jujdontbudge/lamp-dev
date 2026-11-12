# Lamp TypeScript SDK

Generated from `contracts/public/schema.json`; do not edit.

```js
import { LampClient } from "@eilmer/lamp-sdk";
const lamp = new LampClient({ socket: process.env.LAMP_SOCKET, token: process.env.LAMP_TOKEN });
const accepted = await lamp.blobCreate({ size_bytes: 3, media_type: "text/plain" });
const op = await lamp.waitOperation(accepted.operation_id);
```

Every method maps to one canonical operation (`OPERATIONS`). Errors throw `LampError` with the Lamp error code.
