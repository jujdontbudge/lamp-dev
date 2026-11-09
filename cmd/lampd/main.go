// lampd is a build boundary only. No listener, authority or privileged setup.
package main

import (
	"fmt"
	"os"
)

func main() { fmt.Fprintln(os.Stderr, "lampd control service is not implemented (T3)"); os.Exit(1) }
