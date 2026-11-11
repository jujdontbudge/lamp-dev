# Lamp Python SDK

Generated from `contracts/public/schema.json`; do not edit. Standard library only.

```python
from lamp_sdk import LampClient
lamp = LampClient(socket=os.environ["LAMP_SOCKET"], token=os.environ["LAMP_TOKEN"])
accepted = lamp.blob_create(size_bytes=3, media_type="text/plain")
op = lamp.wait_operation(accepted["operation_id"])
```
