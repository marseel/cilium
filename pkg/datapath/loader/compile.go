// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package loader

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/asm"
	"github.com/sirupsen/logrus"

	"github.com/cilium/cilium/pkg/command/exec"
	"github.com/cilium/cilium/pkg/datapath/linux/probes"
	"github.com/cilium/cilium/pkg/datapath/types"
	"github.com/cilium/cilium/pkg/lock"
	"github.com/cilium/cilium/pkg/logging/logfields"
	"github.com/cilium/cilium/pkg/option"
)

// outputType determines the type to be generated by the compilation steps.
type outputType string

const (
	outputObject = outputType("obj")
	outputSource = outputType("c")

	compiler = "clang"

	endpointPrefix = "bpf_lxc"
	endpointProg   = endpointPrefix + "." + string(outputSource)
	endpointObj    = endpointPrefix + ".o"

	hostEndpointPrefix       = "bpf_host"
	hostEndpointNetdevPrefix = "bpf_netdev_"
	hostEndpointProg         = hostEndpointPrefix + "." + string(outputSource)
	hostEndpointObj          = hostEndpointPrefix + ".o"

	networkPrefix = "bpf_network"
	networkProg   = networkPrefix + "." + string(outputSource)
	networkObj    = networkPrefix + ".o"

	xdpPrefix = "bpf_xdp"
	xdpProg   = xdpPrefix + "." + string(outputSource)
	xdpObj    = xdpPrefix + ".o"

	overlayPrefix = "bpf_overlay"
	overlayProg   = overlayPrefix + "." + string(outputSource)
	overlayObj    = overlayPrefix + ".o"

	wireguardPrefix = "bpf_wireguard"
	wireguardProg   = wireguardPrefix + "." + string(outputSource)
	wireguardObj    = wireguardPrefix + ".o"
)

var (
	probeCPUOnce sync.Once

	// default fallback
	nameBPFCPU = "v1"
)

// progInfo describes a program to be compiled with the expected output format
type progInfo struct {
	// Source is the program source (base) filename to be compiled
	Source string
	// Output is the expected (base) filename produced from the source
	Output string
	// OutputType to be created by LLVM
	OutputType outputType
	// Options are passed directly to LLVM as individual parameters
	Options []string
}

func (pi *progInfo) AbsoluteOutput(dir *directoryInfo) string {
	return filepath.Join(dir.Output, pi.Output)
}

// directoryInfo includes relevant directories for compilation and linking
type directoryInfo struct {
	// Library contains the library code to be used for compilation
	Library string
	// Runtime contains headers for compilation
	Runtime string
	// State contains node, lxc, and features headers for templatization
	State string
	// Output is the directory where the files will be stored
	Output string
}

var (
	standardCFlags = []string{"-O2", "--target=bpf", "-std=gnu89",
		"-nostdinc",
		"-Wall", "-Wextra", "-Werror", "-Wshadow",
		"-Wno-address-of-packed-member",
		"-Wno-unknown-warning-option",
		"-Wno-gnu-variable-sized-type-not-at-end",
		"-Wdeclaration-after-statement",
		"-Wimplicit-int-conversion",
		"-Wenum-conversion"}

	// testIncludes allows the unit tests to inject additional include
	// paths into the compile command at test time. It is usually nil.
	testIncludes []string

	epProg = &progInfo{
		Source:     endpointProg,
		Output:     endpointObj,
		OutputType: outputObject,
	}
	hostEpProg = &progInfo{
		Source:     hostEndpointProg,
		Output:     hostEndpointObj,
		OutputType: outputObject,
	}
	networkTcProg = &progInfo{
		Source:     networkProg,
		Output:     networkObj,
		OutputType: outputObject,
	}
)

// getBPFCPU returns the BPF CPU for this host.
func getBPFCPU() string {
	probeCPUOnce.Do(func() {
		if !option.Config.DryMode {
			// We can probe the availability of BPF instructions indirectly
			// based on what kernel helpers are available when both were
			// added in the same release.
			// We want to enable v3 only on kernels 5.10+ where we have
			// tested it and need it to work around complexity issues.
			if probes.HaveV3ISA() == nil {
				if probes.HaveProgramHelper(ebpf.SchedCLS, asm.FnRedirectNeigh) == nil {
					nameBPFCPU = "v3"
					return
				}
			}
			// We want to enable v2 on all kernels that support it, that is,
			// kernels 4.14+.
			if probes.HaveV2ISA() == nil {
				nameBPFCPU = "v2"
			}
		}
	})
	return nameBPFCPU
}

func pidFromProcess(proc *os.Process) string {
	result := "not-started"
	if proc != nil {
		result = fmt.Sprintf("%d", proc.Pid)
	}
	return result
}

// compile and optionally link a program.
//
// May output assembly or source code after prepocessing.
func compile(ctx context.Context, prog *progInfo, dir *directoryInfo) (string, error) {
	possibleCPUs, err := ebpf.PossibleCPU()
	if err != nil {
		return "", fmt.Errorf("failed to get number of possible CPUs: %w", err)
	}

	compileArgs := append(testIncludes,
		fmt.Sprintf("-I%s", path.Join(dir.Runtime, "globals")),
		fmt.Sprintf("-I%s", dir.State),
		fmt.Sprintf("-I%s", dir.Library),
		fmt.Sprintf("-I%s", path.Join(dir.Library, "include")),
	)

	switch prog.OutputType {
	case outputSource:
		compileArgs = append(compileArgs, "-E") // Preprocessor
	case outputObject:
		compileArgs = append(compileArgs, "-g")
	}

	compileArgs = append(compileArgs, standardCFlags...)
	compileArgs = append(compileArgs, fmt.Sprintf("-D__NR_CPUS__=%d", possibleCPUs))
	compileArgs = append(compileArgs, "-mcpu="+getBPFCPU())
	compileArgs = append(compileArgs, prog.Options...)
	compileArgs = append(compileArgs,
		"-c", path.Join(dir.Library, prog.Source),
		"-o", "-", // Always output to stdout
	)

	log.WithFields(logrus.Fields{
		"target": compiler,
		"args":   compileArgs,
	}).Debug("Launching compiler")

	compileCmd, cancelCompile := exec.WithCancel(ctx, compiler, compileArgs...)
	defer cancelCompile()

	output, err := os.Create(prog.AbsoluteOutput(dir))
	if err != nil {
		return "", err
	}
	defer output.Close()
	compileCmd.Stdout = output

	var compilerStderr bytes.Buffer
	compileCmd.Stderr = &compilerStderr

	if err := compileCmd.Run(); err != nil {
		err = fmt.Errorf("Failed to compile %s: %w", prog.Output, err)

		// In linux/unix based implementations, cancelling the context for a cmd.Run() will
		// return errors: "context cancelled" if the context is cancelled prior to the process
		// starting and "signal: killed" if it is already running.
		// This can mess up calling logging logic which expects the returned error to have
		// context.Cancelled so we join this error in to fix that.
		if errors.Is(ctx.Err(), context.Canceled) &&
			compileCmd.ProcessState != nil &&
			!compileCmd.ProcessState.Exited() &&
			strings.HasSuffix(err.Error(), syscall.SIGKILL.String()) {
			err = errors.Join(err, ctx.Err())
		}

		if !errors.Is(err, context.Canceled) {
			log.WithFields(logrus.Fields{
				"compiler-pid": pidFromProcess(compileCmd.Process),
			}).Error(err)
		}

		scanner := bufio.NewScanner(io.LimitReader(&compilerStderr, 1_000_000))
		for scanner.Scan() {
			log.Warn(scanner.Text())
		}

		return "", err
	}

	// Cmd.ProcessState is populated by Cmd.Wait(). Cmd.Run() bails out if
	// Cmd.Start() fails, which will leave Cmd.ProcessState nil. Only log peak
	// RSS if the compilation succeeded, which will be the majority of cases.
	if usage, ok := compileCmd.ProcessState.SysUsage().(*syscall.Rusage); ok {
		log.WithFields(logrus.Fields{
			"compiler-pid": compileCmd.Process.Pid,
			"output":       output.Name(),
		}).Debugf("Compilation had peak RSS of %d bytes", usage.Maxrss)
	}

	return output.Name(), nil
}

// compileDatapath invokes the compiler and linker to create all state files for
// the BPF datapath, with the primary target being the BPF ELF binary.
//
// It also creates the following output files:
// * Preprocessed C
// * Assembly
// * Object compiled with debug symbols
func compileDatapath(ctx context.Context, dirs *directoryInfo, isHost bool, logger *logrus.Entry) error {
	scopedLog := logger.WithField(logfields.Debug, true)

	versionCmd := exec.CommandContext(ctx, compiler, "--version")
	compilerVersion, err := versionCmd.CombinedOutput(scopedLog, true)
	if err != nil {
		return err
	}
	scopedLog.WithFields(logrus.Fields{
		compiler: string(compilerVersion),
	}).Debug("Compiling datapath")

	prog := epProg
	if isHost {
		prog = hostEpProg
	}

	if option.Config.Debug && prog.OutputType == outputObject {
		// Write out preprocessing files for debugging purposes
		debugProg := *prog
		debugProg.Output = debugProg.Source
		debugProg.OutputType = outputSource

		if _, err := compile(ctx, &debugProg, dirs); err != nil {
			// Only log an error here if the context was not canceled. This log message
			// should only represent failures with respect to compiling the program.
			if !errors.Is(err, context.Canceled) {
				scopedLog.WithField(logfields.Params, logfields.Repr(debugProg)).WithError(err).Debug("JoinEP: Failed to compile")
			}
			return err
		}
	}

	if _, err := compile(ctx, prog, dirs); err != nil {
		// Only log an error here if the context was not canceled. This log message
		// should only represent failures with respect to compiling the program.
		if !errors.Is(err, context.Canceled) {
			scopedLog.WithField(logfields.Params, logfields.Repr(prog)).WithError(err).Warn("JoinEP: Failed to compile")
		}
		return err
	}

	return nil
}

// compileWithOptions compiles a BPF program generating an object file,
// using a set of provided compiler options.
func compileWithOptions(ctx context.Context, src string, out string, opts []string) error {
	prog := progInfo{
		Source:     src,
		Options:    opts,
		Output:     out,
		OutputType: outputObject,
	}
	dirs := directoryInfo{
		Library: option.Config.BpfDir,
		Runtime: option.Config.StateDir,
		Output:  option.Config.StateDir,
		State:   option.Config.StateDir,
	}
	_, err := compile(ctx, &prog, &dirs)
	return err
}

// compileDefault compiles a BPF program generating an object file with default options.
func compileDefault(ctx context.Context, src string, out string) error {
	return compileWithOptions(ctx, src, out, nil)
}

// compileNetwork compiles a BPF program attached to network
func compileNetwork(ctx context.Context) error {
	dirs := directoryInfo{
		Library: option.Config.BpfDir,
		Runtime: option.Config.StateDir,
		Output:  option.Config.StateDir,
		State:   option.Config.StateDir,
	}
	scopedLog := log.WithField(logfields.Debug, true)

	versionCmd := exec.CommandContext(ctx, compiler, "--version")
	compilerVersion, err := versionCmd.CombinedOutput(scopedLog, true)
	if err != nil {
		return err
	}
	scopedLog.WithFields(logrus.Fields{
		compiler: string(compilerVersion),
	}).Debug("Compiling network programs")

	// Write out assembly and preprocessing files for debugging purposes
	if _, err := compile(ctx, networkTcProg, &dirs); err != nil {
		scopedLog.WithField(logfields.Params, logfields.Repr(networkTcProg)).
			WithError(err).Warn("Failed to compile")
		return err
	}
	return nil
}

// compileOverlay compiles BPF programs in bpf_overlay.c.
func compileOverlay(ctx context.Context, opts []string) error {
	dirs := &directoryInfo{
		Library: option.Config.BpfDir,
		Runtime: option.Config.StateDir,
		Output:  option.Config.StateDir,
		State:   option.Config.StateDir,
	}
	scopedLog := log.WithField(logfields.Debug, true)

	versionCmd := exec.CommandContext(ctx, compiler, "--version")
	compilerVersion, err := versionCmd.CombinedOutput(scopedLog, true)
	if err != nil {
		return err
	}
	scopedLog.WithFields(logrus.Fields{
		compiler: string(compilerVersion),
	}).Debug("Compiling overlay programs")

	prog := &progInfo{
		Source:     overlayProg,
		Output:     overlayObj,
		OutputType: outputObject,
		Options:    opts,
	}
	// Write out assembly and preprocessing files for debugging purposes
	if _, err := compile(ctx, prog, dirs); err != nil {
		scopedLog.WithField(logfields.Params, logfields.Repr(prog)).
			WithError(err).Warn("Failed to compile")
		return err
	}
	return nil
}

func compileWireguard(ctx context.Context) (err error) {
	dirs := &directoryInfo{
		Library: option.Config.BpfDir,
		Runtime: option.Config.StateDir,
		Output:  option.Config.StateDir,
		State:   option.Config.StateDir,
	}
	scopedLog := log.WithField(logfields.Debug, true)

	versionCmd := exec.CommandContext(ctx, compiler, "--version")
	compilerVersion, err := versionCmd.CombinedOutput(scopedLog, true)
	if err != nil {
		return err
	}
	scopedLog.WithFields(logrus.Fields{
		compiler: string(compilerVersion),
	}).Debug("Compiling wireguard programs")

	prog := &progInfo{
		Source:     wireguardProg,
		Output:     wireguardObj,
		OutputType: outputObject,
	}
	// Write out assembly and preprocessing files for debugging purposes
	if _, err := compile(ctx, prog, dirs); err != nil {
		scopedLog.WithField(logfields.Params, logfields.Repr(prog)).
			WithError(err).Warn("Failed to compile")
		return err
	}
	return nil
}

type compilationLock struct {
	lock.RWMutex
}

func NewCompilationLock() types.CompilationLock {
	return &compilationLock{}
}
