// Package fsys is a thin interface over os.Root and flock, with a real
// implementation (OS) and a fault-injecting one (Fault). It is the only
// package that touches the disk.
//
// Errors are returned as the OS gives them — *os.PathError or *os.LinkError
// around a syscall.Errno, with paths relative to the root — because each call
// site in store decides what an errno means there (implementation-spec.md, OS
// errors).
package fsys
