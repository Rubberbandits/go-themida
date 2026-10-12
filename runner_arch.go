package themida

// Arch selects which Themida executable to run when [Runner.Path] names an
// installation directory rather than a specific executable. Themida ships a
// 32-bit and a 64-bit protector, and the architecture must match the input
// binary being protected.
type Arch uint8

const (
	// Arch32 selects the 32-bit protector, Themida.exe.
	Arch32 Arch = 32
	// Arch64 selects the 64-bit protector, Themida64.exe.
	Arch64 Arch = 64
)

// architectureExecutable returns the protector executable file name for arch,
// defaulting to the 64-bit executable for any value other than [Arch32].
func architectureExecutable(arch Arch) string {
	if arch == Arch32 {
		return "Themida.exe"
	}

	return "Themida64.exe"
}
