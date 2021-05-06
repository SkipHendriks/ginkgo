package integration_test

import (
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"
)

type ProfileLine struct {
	Index   int
	Caller  string
	CumStat float64
}

type ProfileLines []ProfileLine

func (lines ProfileLines) FindCaller(caller string) ProfileLine {
	for _, line := range lines {
		if strings.Contains(line.Caller, caller) {
			return line
		}
	}

	Fail(fmt.Sprintf("Could not find caller %s among profile lines %+v.", caller, lines), 1)
	return ProfileLine{}
}

var PROFILE_RE = regexp.MustCompile(`[\d\.]+[MBms]*\s*[\d\.]+\%\s*[\d\.]+\%\s*([\d\.]+[MBnms]*)\s*[\d\.]+\%\s*(.*)`)

func ParseProfile(binary string, path string) ProfileLines {
	cmd := exec.Command("go", "tool", "pprof", "-cum", "-top", binary, path)
	output, err := cmd.CombinedOutput()
	GinkgoWriter.Printf("Profile for: %s\n%s\n", path, string(output))
	ExpectWithOffset(1, err).ShouldNot(HaveOccurred())
	out := ProfileLines{}
	idx := 0
	for _, line := range strings.Split(string(output), "\n") {
		matches := PROFILE_RE.FindStringSubmatch(line)
		if matches == nil {
			continue
		}
		cumStatEntry := matches[1]
		var cumStat float64
		if strings.Contains(cumStatEntry, "MB") {
			var err error
			cumStat, err = strconv.ParseFloat(strings.TrimRight(cumStatEntry, "MB"), 64)
			ExpectWithOffset(1, err).ShouldNot(HaveOccurred())
		} else {
			duration, err := time.ParseDuration(cumStatEntry)
			ExpectWithOffset(1, err).ShouldNot(HaveOccurred())
			cumStat = float64(duration.Milliseconds())
		}
		out = append(out, ProfileLine{
			Index:   idx,
			Caller:  matches[2],
			CumStat: cumStat,
		})
		idx += 1
	}
	return out
}

var _ = Describe("Profiling Specs", func() {
	Describe("Measuring code coverage", func() {
		BeforeEach(func() {
			fm.MountFixture("coverage")
		})

		processCoverageProfile := func(path string) string {
			profileOutput, err := exec.Command("go", "tool", "cover", fmt.Sprintf("-func=%s", path)).CombinedOutput()
			ExpectWithOffset(1, err).ShouldNot(HaveOccurred())
			return string(profileOutput)
		}

		Context("when running a single package in series or in parallel with -cover", func() {
			It("emits the coverage pecentage and generates a cover profile", func() {
				seriesSession := startGinkgo(fm.PathTo("coverage"), "--no-color", "-cover")
				Eventually(seriesSession).Should(gexec.Exit(0))
				Ω(seriesSession.Out).Should(gbytes.Say(`coverage: 80\.0% of statements`))
				seriesCoverage := processCoverageProfile(fm.PathTo("coverage", "coverprofile.out"))
				fm.RemoveFile("coverage", "coverprofile.out")

				parallelSession := startGinkgo(fm.PathTo("coverage"), "--no-color", "-nodes=2", "-cover")
				Eventually(parallelSession).Should(gexec.Exit(0))
				Ω(parallelSession.Out).Should(gbytes.Say(`coverage: 80\.0% of statements`))
				parallelCoverage := processCoverageProfile(fm.PathTo("coverage", "coverprofile.out"))

				Ω(parallelCoverage).Should(Equal(seriesCoverage))
			})
		})

		Context("with -coverpkg", func() {
			It("computes coverage of the passed-in additional packages", func() {
				coverPkgFlag := fmt.Sprintf("-coverpkg=%s,%s", fm.PackageNameFor("coverage"), fm.PackageNameFor("coverage/external_coverage"))
				seriesSession := startGinkgo(fm.PathTo("coverage"), coverPkgFlag)
				Eventually(seriesSession).Should(gexec.Exit(0))
				Ω(seriesSession.Out).Should(gbytes.Say("coverage: 71.4% of statements in"))
				seriesCoverage := processCoverageProfile(fm.PathTo("coverage", "coverprofile.out"))
				fm.RemoveFile("coverage", "coverprofile.out")

				parallelSession := startGinkgo(fm.PathTo("coverage"), "--no-color", "-nodes=2", coverPkgFlag)
				Eventually(parallelSession).Should(gexec.Exit(0))
				Ω(parallelSession.Out).Should(gbytes.Say(`coverage: 71\.4% of statements`))
				parallelCoverage := processCoverageProfile(fm.PathTo("coverage", "coverprofile.out"))

				Ω(parallelCoverage).Should(Equal(seriesCoverage))
			})
		})

		Context("with a custom profile name", func() {
			It("generates cover profiles with the specified name", func() {
				session := startGinkgo(fm.PathTo("coverage"), "--no-color", "-coverprofile=myprofile.out")
				Eventually(session).Should(gexec.Exit(0))
				Ω(session.Out).Should(gbytes.Say(`coverage: 80\.0% of statements`))
				Ω(fm.PathTo("coverage", "myprofile.out")).Should(BeAnExistingFile())
				Ω(fm.PathTo("coverage", "coverprofile.out")).ShouldNot(BeAnExistingFile())
			})
		})

		Context("when multiple suites are tested", func() {
			BeforeEach(func() {
				fm.MountFixture("combined_coverage")
			})

			It("generates a single cover profile", func() {
				session := startGinkgo(fm.PathTo("combined_coverage"), "--no-color", "--cover", "-r", "-nodes=2", "--covermode=atomic")
				Eventually(session).Should(gexec.Exit(0))
				Ω(fm.PathTo("combined_coverage", "coverprofile.out")).Should(BeAnExistingFile())
				Ω(fm.PathTo("combined_coverage", "first_package/coverprofile.out")).ShouldNot(BeAnExistingFile())
				Ω(fm.PathTo("combined_coverage", "second_package/coverprofile.out")).ShouldNot(BeAnExistingFile())
				Ω(fm.PathTo("combined_coverage", "third_package/coverprofile.out")).ShouldNot(BeAnExistingFile())

				Ω(session.Out).Should(gbytes.Say(`coverage: 80\.0% of statements`))
				Ω(session.Out).Should(gbytes.Say(`coverage: 100\.0% of statements`))
				Ω(session.Out).Should(gbytes.Say(`coverage: \[no statements\]`))

				By("ensuring there is only one 'mode:' line")
				re := regexp.MustCompile(`mode: atomic`)
				content := fm.ContentOf("combined_coverage", "coverprofile.out")
				matches := re.FindAllStringIndex(content, -1)
				Ω(len(matches)).Should(Equal(1))

				By("emitting a composite coverage score")
				Ω(session.Out).Should(gbytes.Say(`composite coverage: 90\.0% of statements`))
			})

			Context("when -keep-separate-coverprofiles is set", func() {
				It("generates separate coverprofiles", func() {
					session := startGinkgo(fm.PathTo("combined_coverage"), "--no-color", "--cover", "-r", "-nodes=2", "--keep-separate-coverprofiles")
					Eventually(session).Should(gexec.Exit(0))
					Ω(fm.PathTo("combined_coverage", "coverprofile.out")).ShouldNot(BeAnExistingFile())
					Ω(fm.PathTo("combined_coverage", "first_package/coverprofile.out")).Should(BeAnExistingFile())
					Ω(fm.PathTo("combined_coverage", "second_package/coverprofile.out")).Should(BeAnExistingFile())
					Ω(fm.PathTo("combined_coverage", "third_package/coverprofile.out")).Should(BeAnExistingFile())
				})
			})
		})

		Context("when -output-dir is set", func() {
			BeforeEach(func() {
				fm.MountFixture("combined_coverage")
			})

			It("puts the cover profile in -output-dir", func() {
				session := startGinkgo(fm.PathTo("combined_coverage"), "--no-color", "--cover", "-r", "-nodes=2", "--output-dir=./output")
				Eventually(session).Should(gexec.Exit(0))
				Ω(fm.PathTo("combined_coverage", "output/coverprofile.out")).Should(BeAnExistingFile())

				By("emitting a composite coverage score")
				Ω(session.Out).Should(gbytes.Say(`composite coverage: 90\.0% of statements`))
			})

			Context("when -keep-separate-coverprofiles is set", func() {
				It("puts namespaced coverprofiels in the -output-dir", func() {
					session := startGinkgo(fm.PathTo("combined_coverage"), "--no-color", "--cover", "-r", "-nodes=2", "--output-dir=./output", "--keep-separate-coverprofiles")
					Eventually(session).Should(gexec.Exit(0))
					Ω(fm.PathTo("combined_coverage", "output/coverprofile.out")).ShouldNot(BeAnExistingFile())
					Ω(fm.PathTo("combined_coverage", "output/first_package_coverprofile.out")).Should(BeAnExistingFile())
					Ω(fm.PathTo("combined_coverage", "output/second_package_coverprofile.out")).Should(BeAnExistingFile())
				})
			})
		})
	})

	Describe("measuring cpu, memory, block, and mutex profiles", func() {
		BeforeEach(func() {
			fm.MountFixture("profile")
		})

		DescribeTable("profile behavior",
			func(pathToBinary func(string) string, pathToProfile func(string, string) string, args ...string) {
				args = append([]string{"--no-color", "-r", "--cpuprofile=cpu.out", "--memprofile=mem.out", "--blockprofile=block.out", "--mutexprofile=mutex.out"}, args...)
				session := startGinkgo(fm.PathTo("profile"), args...)
				Eventually(session).Should(gexec.Exit(0))

				// Verify that the binaries have been preserved and the profiles were generated
				for _, pkg := range []string{"slow_memory_hog", "block_contest", "lock_contest"} {
					Ω(pathToBinary(pkg)).Should(BeAnExistingFile())
					for _, profile := range []string{"cpu.out", "mem.out", "block.out", "mutex.out"} {
						Ω(pathToProfile(pkg, profile)).Should(BeAnExistingFile())
					}
				}

				cpuProfile := ParseProfile(pathToBinary("slow_memory_hog"), pathToProfile("slow_memory_hog", "cpu.out"))
				// The CPUProfile for the slow_memory_hog test should list the slow_memory_hog.SomethingExpensive functions as one of the most time-consuming functions
				// we can't assert on time as that will vary from machine to machine, however we _can_ assert that the slow_memory_hog.SomethingExpensive
				// function has a low index
				Ω(cpuProfile.FindCaller("slow_memory_hog.SomethingExpensive").Index).Should(BeNumerically("<=", 10))

				memProfile := ParseProfile(pathToBinary("slow_memory_hog"), pathToProfile("slow_memory_hog", "mem.out"))
				// The MemProifle for the slow_memory_hog test should list the slow_memory_hog.SomethingExpensive functions as one of the most memory-consuming functions
				// Assrting on the amount of memory consumed should be stable across tests as the function always builds a large array of this size
				Ω(memProfile.FindCaller("slow_memory_hog.SomethingExpensive").CumStat).Should(BeNumerically(">=", 200))

				blockProfile := ParseProfile(pathToBinary("block_contest"), pathToProfile("block_contest", "block.out"))
				// The BlockProfile for the block_contest test should list two channel-reading functions:
				// block_contest.ReadTheChannel is called 10 times and takes ~5ms per call
				// block_contest.SlowReadTheChannel is called once and teakes ~500ms per call
				// Asserting that both times are within a range should be stable across tests
				// Note: these numbers are adjusted slightly to tolerate variance during test runs
				Ω(blockProfile.FindCaller("block_contest.ReadTheChannel").CumStat).Should(BeNumerically(">=", 45))
				Ω(blockProfile.FindCaller("block_contest.ReadTheChannel").CumStat).Should(BeNumerically("<", 500))
				Ω(blockProfile.FindCaller("block_contest.SlowReadTheChannel").CumStat).Should(BeNumerically(">=", 450))

				mutexProfile := ParseProfile(pathToBinary("lock_contest"), pathToProfile("lock_contest", "mutex.out"))
				// The MutexProfile for the lock_contest test should list two functions that wait on a lock.
				// Unfortunately go doesn't seem to capture the names of the functions - so they're listed here as
				// lock_contest_test.glob..func1.1 is called 10 times and takes ~5ms per call
				// lock_contest_test.glob..func2.1 is called once and teakes ~500ms per call
				// Asserting that both times are within a range should be stable across tests.  The function names should be as well
				// but that might become a source of failure in the future
				// Note: these numbers are adjusted slightly to tolerate variance during test runs
				Ω(mutexProfile.FindCaller("lock_contest_test.glob..func1.1").CumStat).Should(BeNumerically(">=", 45))
				Ω(mutexProfile.FindCaller("lock_contest_test.glob..func1.1").CumStat).Should(BeNumerically("<", 500))
				Ω(mutexProfile.FindCaller("lock_contest_test.glob..func2.1").CumStat).Should(BeNumerically(">=", 450))
			},

			Entry("when running in series",
				func(pkg string) string { return fm.PathTo("profile", pkg+"/"+pkg+".test") },
				func(pkg string, profile string) string { return fm.PathTo("profile", pkg+"/"+profile) },
			),

			Entry("when running in parallel",
				func(pkg string) string { return fm.PathTo("profile", pkg+"/"+pkg+".test") },
				func(pkg string, profile string) string { return fm.PathTo("profile", pkg+"/"+profile) },
				"-nodes=3",
			),

			Entry("when --output-dir is set",
				func(pkg string) string { return fm.PathTo("profile", "profiles/"+pkg+".test") },
				func(pkg string, profile string) string { return fm.PathTo("profile", "profiles/"+pkg+"_"+profile) },
				"-nodes=3", "--output-dir=./profiles",
			),
		)
	})
})