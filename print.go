package main

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Jguer/yay/v9/generic"
	rpc "github.com/mikkeloscar/aur"
)

func (warnings *aurWarnings) print() {
	if len(warnings.Missing) > 0 {
		fmt.Print(generic.Bold(generic.Yellow(generic.SmallArrow)) + " Missing AUR Packages:")
		for _, name := range warnings.Missing {
			fmt.Print("  " + generic.Cyan(name))
		}
		fmt.Println()
	}

	if len(warnings.Orphans) > 0 {
		fmt.Print(generic.Bold(generic.Yellow(generic.SmallArrow)) + " Orphaned AUR Packages:")
		for _, name := range warnings.Orphans {
			fmt.Print("  " + generic.Cyan(name))
		}
		fmt.Println()
	}

	if len(warnings.OutOfDate) > 0 {
		fmt.Print(generic.Bold(generic.Yellow(generic.SmallArrow)) + " Out Of Date AUR Packages:")
		for _, name := range warnings.OutOfDate {
			fmt.Print("  " + generic.Cyan(name))
		}
		fmt.Println()
	}

}

// PrintSearch handles printing search results in a given format
func (q aurQuery) printSearch(start int) {
	localDB, _ := alpmHandle.LocalDB()

	for i, res := range q {
		var toprint string
		if config.SearchMode == numberMenu {
			if config.SortMode == bottomUp {
				toprint += generic.Magenta(strconv.Itoa(len(q)+start-i-1) + " ")
			} else {
				toprint += generic.Magenta(strconv.Itoa(start+i) + " ")
			}
		} else if config.SearchMode == minimal {
			fmt.Println(res.Name)
			continue
		}

		toprint += generic.Bold(generic.ColourHash("aur")) + "/" + generic.Bold(res.Name) +
			" " + generic.Cyan(res.Version) +
			generic.Bold(" (+"+strconv.Itoa(res.NumVotes)) +
			" " + generic.Bold(strconv.FormatFloat(res.Popularity, 'f', 2, 64)+"%) ")

		if res.Maintainer == "" {
			toprint += generic.Bold(generic.Red("(Orphaned)")) + " "
		}

		if res.OutOfDate != 0 {
			toprint += generic.Bold(generic.Red("(Out-of-date "+formatTime(res.OutOfDate)+")")) + " "
		}

		if pkg := localDB.Pkg(res.Name); pkg != nil {
			if pkg.Version() != res.Version {
				toprint += generic.Bold(generic.Green("(Installed: " + pkg.Version() + ")"))
			} else {
				toprint += generic.Bold(generic.Green("(Installed)"))
			}
		}
		toprint += "\n    " + res.Description
		fmt.Println(toprint)
	}
}

// PrintSearch receives a RepoSearch type and outputs pretty text.
func (s repoQuery) printSearch() {
	for i, res := range s {
		var toprint string
		if config.SearchMode == numberMenu {
			if config.SortMode == bottomUp {
				toprint += generic.Magenta(strconv.Itoa(len(s)-i) + " ")
			} else {
				toprint += generic.Magenta(strconv.Itoa(i+1) + " ")
			}
		} else if config.SearchMode == minimal {
			fmt.Println(res.Name())
			continue
		}

		toprint += generic.Bold(generic.ColourHash(res.DB().Name())) + "/" + generic.Bold(res.Name()) +
			" " + generic.Cyan(res.Version()) +
			generic.Bold(" ("+generic.Human(res.Size())+
				" "+generic.Human(res.ISize())+") ")

		if len(res.Groups().Slice()) != 0 {
			toprint += fmt.Sprint(res.Groups().Slice(), " ")
		}

		localDB, err := alpmHandle.LocalDB()
		if err == nil {
			if pkg := localDB.Pkg(res.Name()); pkg != nil {
				if pkg.Version() != res.Version() {
					toprint += generic.Bold(generic.Green("(Installed: " + pkg.Version() + ")"))
				} else {
					toprint += generic.Bold(generic.Green("(Installed)"))
				}
			}
		}

		toprint += "\n    " + res.Description()
		fmt.Println(toprint)
	}
}

// Pretty print a set of packages from the same package base.
// Packages foo and bar from a pkgbase named base would print like so:
// base (foo bar)
func (base Base) String() string {
	pkg := base[0]
	str := pkg.PackageBase
	if len(base) > 1 || pkg.PackageBase != pkg.Name {
		str2 := " ("
		for _, split := range base {
			str2 += split.Name + " "
		}
		str2 = str2[:len(str2)-1] + ")"

		str += str2
	}

	return str
}

func (u upgrade) StylizedNameWithRepository() string {
	return generic.Bold(generic.ColourHash(u.Repository)) + "/" + generic.Bold(u.Name)
}

// Print prints the details of the packages to upgrade.
func (u upSlice) print() {
	longestName, longestVersion := 0, 0
	for _, pack := range u {
		packNameLen := len(pack.StylizedNameWithRepository())
		version, _ := getVersionDiff(pack.LocalVersion, pack.RemoteVersion)
		packVersionLen := len(version)
		longestName = generic.Max(packNameLen, longestName)
		longestVersion = generic.Max(packVersionLen, longestVersion)
	}

	namePadding := fmt.Sprintf("%%-%ds  ", longestName)
	versionPadding := fmt.Sprintf("%%-%ds", longestVersion)
	numberPadding := fmt.Sprintf("%%%dd  ", len(fmt.Sprintf("%v", len(u))))

	for k, i := range u {
		left, right := getVersionDiff(i.LocalVersion, i.RemoteVersion)

		fmt.Print(generic.Magenta(fmt.Sprintf(numberPadding, len(u)-k)))

		fmt.Printf(namePadding, i.StylizedNameWithRepository())

		fmt.Printf("%s -> %s\n", fmt.Sprintf(versionPadding, left), right)
	}
}

// Print prints repository packages to be downloaded
func (do *depOrder) Print() {
	repo := ""
	repoMake := ""
	aur := ""
	aurMake := ""

	repoLen := 0
	repoMakeLen := 0
	aurLen := 0
	aurMakeLen := 0

	for _, pkg := range do.Repo {
		if do.Runtime.Get(pkg.Name()) {
			repo += "  " + pkg.Name() + "-" + pkg.Version()
			repoLen++
		} else {
			repoMake += "  " + pkg.Name() + "-" + pkg.Version()
			repoMakeLen++
		}
	}

	for _, base := range do.Aur {
		pkg := base.Pkgbase()
		pkgStr := "  " + pkg + "-" + base[0].Version
		pkgStrMake := pkgStr

		push := false
		pushMake := false

		switch {
		case len(base) > 1, pkg != base[0].Name:
			pkgStr += " ("
			pkgStrMake += " ("

			for _, split := range base {
				if do.Runtime.Get(split.Name) {
					pkgStr += split.Name + " "
					aurLen++
					push = true
				} else {
					pkgStrMake += split.Name + " "
					aurMakeLen++
					pushMake = true
				}
			}

			pkgStr = pkgStr[:len(pkgStr)-1] + ")"
			pkgStrMake = pkgStrMake[:len(pkgStrMake)-1] + ")"
		case do.Runtime.Get(base[0].Name):
			aurLen++
			push = true
		default:
			aurMakeLen++
			pushMake = true
		}

		if push {
			aur += pkgStr
		}
		if pushMake {
			aurMake += pkgStrMake
		}
	}

	printDownloads("Repo", repoLen, repo)
	printDownloads("Repo Make", repoMakeLen, repoMake)
	printDownloads("Aur", aurLen, aur)
	printDownloads("Aur Make", aurMakeLen, aurMake)
}

func printDownloads(repoName string, length int, packages string) {
	if length < 1 {
		return
	}

	repoInfo := generic.Bold(generic.Blue(
		"[" + repoName + ": " + strconv.Itoa(length) + "]"))
	fmt.Println(repoInfo + generic.Cyan(packages))
}

func printInfoValue(str, value string) {
	if value == "" {
		value = "None"
	}

	fmt.Printf(generic.Bold("%-16s%s")+" %s\n", str, ":", value)
}

// PrintInfo prints package info like pacman -Si.
func PrintInfo(a *rpc.Pkg) {
	printInfoValue("Repository", "aur")
	printInfoValue("Name", a.Name)
	printInfoValue("Keywords", strings.Join(a.Keywords, "  "))
	printInfoValue("Version", a.Version)
	printInfoValue("Description", a.Description)
	printInfoValue("URL", a.URL)
	printInfoValue("AUR URL", config.AURURL+"/packages/"+a.Name)
	printInfoValue("Groups", strings.Join(a.Groups, "  "))
	printInfoValue("Licenses", strings.Join(a.License, "  "))
	printInfoValue("Provides", strings.Join(a.Provides, "  "))
	printInfoValue("Depends On", strings.Join(a.Depends, "  "))
	printInfoValue("Make Deps", strings.Join(a.MakeDepends, "  "))
	printInfoValue("Check Deps", strings.Join(a.CheckDepends, "  "))
	printInfoValue("Optional Deps", strings.Join(a.OptDepends, "  "))
	printInfoValue("Conflicts With", strings.Join(a.Conflicts, "  "))
	printInfoValue("Maintainer", a.Maintainer)
	printInfoValue("Votes", fmt.Sprintf("%d", a.NumVotes))
	printInfoValue("Popularity", fmt.Sprintf("%f", a.Popularity))
	printInfoValue("First Submitted", formatTimeQuery(a.FirstSubmitted))
	printInfoValue("Last Modified", formatTimeQuery(a.LastModified))

	if a.OutOfDate != 0 {
		printInfoValue("Out-of-date", "Yes ["+formatTime(a.OutOfDate)+"]")
	} else {
		printInfoValue("Out-of-date", "No")
	}

	if cmdArgs.existsDouble("i") {
		printInfoValue("ID", fmt.Sprintf("%d", a.ID))
		printInfoValue("Package Base ID", fmt.Sprintf("%d", a.PackageBaseID))
		printInfoValue("Package Base", a.PackageBase)
		printInfoValue("Snapshot URL", config.AURURL+a.URLPath)
	}

	fmt.Println()
}

// BiggestPackages prints the name of the ten biggest packages in the system.
func biggestPackages() {
	localDB, err := alpmHandle.LocalDB()
	if err != nil {
		return
	}

	pkgCache := localDB.PkgCache()
	pkgS := pkgCache.SortBySize().Slice()

	if len(pkgS) < 10 {
		return
	}

	for i := 0; i < 10; i++ {
		fmt.Println(generic.Bold(pkgS[i].Name()) + ": " + generic.Cyan(generic.Human(pkgS[i].ISize())))
	}
	// Could implement size here as well, but we just want the general idea
}

// localStatistics prints installed packages statistics.
func localStatistics() error {
	info, err := statistics()
	if err != nil {
		return err
	}

	_, _, _, remoteNames, err := filterPackages()
	if err != nil {
		return err
	}

	fmt.Printf(generic.Bold("Yay version v%s\n"), version)
	fmt.Println(generic.Bold(generic.Cyan("===========================================")))
	fmt.Println(generic.Bold(generic.Green("Total installed packages: ")) + generic.Cyan(strconv.Itoa(info.Totaln)))
	fmt.Println(generic.Bold(generic.Green("Total foreign installed packages: ")) + generic.Cyan(strconv.Itoa(len(remoteNames))))
	fmt.Println(generic.Bold(generic.Green("Explicitly installed packages: ")) + generic.Cyan(strconv.Itoa(info.Expln)))
	fmt.Println(generic.Bold(generic.Green("Total Size occupied by packages: ")) + generic.Cyan(generic.Human(info.TotalSize)))
	fmt.Println(generic.Bold(generic.Cyan("===========================================")))
	fmt.Println(generic.Bold(generic.Green("Ten biggest packages:")))
	biggestPackages()
	fmt.Println(generic.Bold(generic.Cyan("===========================================")))

	aurInfoPrint(remoteNames)

	return nil
}

//TODO: Make it less hacky
func printNumberOfUpdates() error {
	//todo
	warnings := &aurWarnings{}
	old := os.Stdout // keep backup of the real stdout
	os.Stdout = nil
	aurUp, repoUp, err := upList(warnings)
	os.Stdout = old // restoring the real stdout
	if err != nil {
		return err
	}
	fmt.Println(len(aurUp) + len(repoUp))

	return nil
}

//TODO: Make it less hacky
func printUpdateList(parser *arguments) error {
	targets := generic.SliceToStringSet(parser.targets)
	warnings := &aurWarnings{}
	old := os.Stdout // keep backup of the real stdout
	os.Stdout = nil
	_, _, localNames, remoteNames, err := filterPackages()
	if err != nil {
		return err
	}

	aurUp, repoUp, err := upList(warnings)
	os.Stdout = old // restoring the real stdout
	if err != nil {
		return err
	}

	noTargets := len(targets) == 0

	if !parser.existsArg("m", "foreign") {
		for _, pkg := range repoUp {
			if noTargets || targets.Get(pkg.Name) {
				if parser.existsArg("q", "quiet") {
					fmt.Printf("%s\n", pkg.Name)
				} else {
					fmt.Printf("%s %s -> %s\n", generic.Bold(pkg.Name), generic.Green(pkg.LocalVersion), generic.Green(pkg.RemoteVersion))
				}
				delete(targets, pkg.Name)
			}
		}
	}

	if !parser.existsArg("n", "native") {
		for _, pkg := range aurUp {
			if noTargets || targets.Get(pkg.Name) {
				if parser.existsArg("q", "quiet") {
					fmt.Printf("%s\n", pkg.Name)
				} else {
					fmt.Printf("%s %s -> %s\n", generic.Bold(pkg.Name), generic.Green(pkg.LocalVersion), generic.Green(pkg.RemoteVersion))
				}
				delete(targets, pkg.Name)
			}
		}
	}

	missing := false

outer:
	for pkg := range targets {
		for _, name := range localNames {
			if name == pkg {
				continue outer
			}
		}

		for _, name := range remoteNames {
			if name == pkg {
				continue outer
			}
		}

		fmt.Fprintln(os.Stderr, generic.Red(generic.Bold("error:")), "package '"+pkg+"' was not found")
		missing = true
	}

	if missing {
		return fmt.Errorf("")
	}

	return nil
}

type item struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
	Creator     string `xml:"dc:creator"`
}

func (item item) print(buildTime time.Time) {
	var fd string
	date, err := time.Parse(time.RFC1123Z, item.PubDate)

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	} else {
		fd = formatTime(int(date.Unix()))
		if _, double, _ := cmdArgs.getArg("news", "w"); !double && !buildTime.IsZero() {
			if buildTime.After(date) {
				return
			}
		}
	}

	fmt.Println(generic.Bold(generic.Magenta(fd)), generic.Bold(strings.TrimSpace(item.Title)))
	//fmt.Println(strings.TrimSpace(item.Link))

	if !cmdArgs.existsArg("q", "quiet") {
		desc := strings.TrimSpace(parseNews(item.Description))
		fmt.Println(desc)
	}
}

type channel struct {
	Title         string `xml:"title"`
	Link          string `xml:"link"`
	Description   string `xml:"description"`
	Language      string `xml:"language"`
	Lastbuilddate string `xml:"lastbuilddate"`
	Items         []item `xml:"item"`
}

type rss struct {
	Channel channel `xml:"channel"`
}

func printNewsFeed() error {
	resp, err := http.Get("https://archlinux.org/feeds/news")
	if err != nil {
		return err
	}

	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	rss := rss{}

	d := xml.NewDecoder(bytes.NewReader(body))
	err = d.Decode(&rss)
	if err != nil {
		return err
	}

	buildTime, err := lastBuildTime()
	if err != nil {
		return err
	}

	if config.SortMode == bottomUp {
		for i := len(rss.Channel.Items) - 1; i >= 0; i-- {
			rss.Channel.Items[i].print(buildTime)
		}
	} else {
		for i := 0; i < len(rss.Channel.Items); i++ {
			rss.Channel.Items[i].print(buildTime)
		}
	}

	return nil
}

// Formats a unix timestamp to ISO 8601 date (yyyy-mm-dd)
func formatTime(i int) string {
	t := time.Unix(int64(i), 0)
	return t.Format("2006-01-02")
}

// Formats a unix timestamp to ISO 8601 date (Mon 02 Jan 2006 03:04:05 PM MST)
func formatTimeQuery(i int) string {
	t := time.Unix(int64(i), 0)
	return t.Format("Mon 02 Jan 2006 03:04:05 PM MST")
}

func providerMenu(dep string, providers providers) *rpc.Pkg {
	size := providers.Len()

	fmt.Print(generic.Bold(generic.Cyan(":: ")))
	str := generic.Bold(fmt.Sprintf(generic.Bold("There are %d providers available for %s:"), size, dep))

	size = 1
	str += generic.Bold(generic.Cyan("\n:: ")) + generic.Bold("Repository AUR\n    ")

	for _, pkg := range providers.Pkgs {
		str += fmt.Sprintf("%d) %s ", size, pkg.Name)
		size++
	}

	fmt.Fprintln(os.Stderr, str)

	for {
		fmt.Print("\nEnter a number (default=1): ")

		if config.NoConfirm {
			fmt.Println("1")
			return providers.Pkgs[0]
		}

		reader := bufio.NewReader(os.Stdin)
		numberBuf, overflow, err := reader.ReadLine()

		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			break
		}

		if overflow {
			fmt.Fprintln(os.Stderr, "Input too long")
			continue
		}

		if string(numberBuf) == "" {
			return providers.Pkgs[0]
		}

		num, err := strconv.Atoi(string(numberBuf))
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s invalid number: %s\n", generic.Red("error:"), string(numberBuf))
			continue
		}

		if num < 1 || num > size {
			fmt.Fprintf(os.Stderr, "%s invalid value: %d is not between %d and %d\n", generic.Red("error:"), num, 1, size)
			continue
		}

		return providers.Pkgs[num-1]
	}

	return nil
}
