# Templates use Lamp's own format, not Docker images

Lamp doesn't accept Docker images or Dockerfiles, and never uses Docker to run sandboxes. Templates are built from Lamp's own format or saved from a running sandbox, including the tools an agent installed. This lets the template format be designed purely for fast boot and fork. Running Docker inside a sandbox stays available as an optional kit, because CI jobs need it.
