# Reuse open source only where it's safer or clearly faster

Lamp writes its own components by default. It reuses an open-source component only when that's needed for security, such as a proven VM boundary, or when the component is very likely the fastest option. The goal is the fastest sandbox designed specifically for agents and harnesses. General-purpose components bring design compromises Lamp doesn't want, so "it already exists" isn't enough reason to adopt one.
