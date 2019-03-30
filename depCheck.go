package main

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/Jguer/yay/v9/generic"
	alpm "github.com/jguer/go-alpm"
)

func (dp *depPool) checkInnerConflict(name string, conflict string, conflicts generic.MapStringSet) {
	for _, pkg := range dp.Aur {
		if pkg.Name == name {
			continue
		}

		if satisfiesAur(conflict, pkg) {
			conflicts.Add(name, pkg.Name)
		}
	}

	for _, pkg := range dp.Repo {
		if pkg.Name() == name {
			continue
		}

		if satisfiesRepo(conflict, pkg) {
			conflicts.Add(name, pkg.Name())
		}
	}
}

func (dp *depPool) checkForwardConflict(name string, conflict string, conflicts generic.MapStringSet) {
	dp.LocalDB.PkgCache().ForEach(func(pkg alpm.Package) error {
		if pkg.Name() == name || dp.hasPackage(pkg.Name()) {
			return nil
		}

		if satisfiesRepo(conflict, &pkg) {
			n := pkg.Name()
			if n != conflict {
				n += " (" + conflict + ")"
			}
			conflicts.Add(name, n)
		}

		return nil
	})
}

func (dp *depPool) checkReverseConflict(name string, conflict string, conflicts generic.MapStringSet) {
	for _, pkg := range dp.Aur {
		if pkg.Name == name {
			continue
		}

		if satisfiesAur(conflict, pkg) {
			if name != conflict {
				name += " (" + conflict + ")"
			}

			conflicts.Add(pkg.Name, name)
		}
	}

	for _, pkg := range dp.Repo {
		if pkg.Name() == name {
			continue
		}

		if satisfiesRepo(conflict, pkg) {
			if name != conflict {
				name += " (" + conflict + ")"
			}

			conflicts.Add(pkg.Name(), name)
		}
	}
}

func (dp *depPool) checkInnerConflicts(conflicts generic.MapStringSet) {
	for _, pkg := range dp.Aur {
		for _, conflict := range pkg.Conflicts {
			dp.checkInnerConflict(pkg.Name, conflict, conflicts)
		}
	}

	for _, pkg := range dp.Repo {
		pkg.Conflicts().ForEach(func(conflict alpm.Depend) error {
			dp.checkInnerConflict(pkg.Name(), conflict.String(), conflicts)
			return nil
		})
	}
}

func (dp *depPool) checkForwardConflicts(conflicts generic.MapStringSet) {
	for _, pkg := range dp.Aur {
		for _, conflict := range pkg.Conflicts {
			dp.checkForwardConflict(pkg.Name, conflict, conflicts)
		}
	}

	for _, pkg := range dp.Repo {
		pkg.Conflicts().ForEach(func(conflict alpm.Depend) error {
			dp.checkForwardConflict(pkg.Name(), conflict.String(), conflicts)
			return nil
		})
	}
}

func (dp *depPool) checkReverseConflicts(conflicts generic.MapStringSet) {
	dp.LocalDB.PkgCache().ForEach(func(pkg alpm.Package) error {
		if dp.hasPackage(pkg.Name()) {
			return nil
		}

		pkg.Conflicts().ForEach(func(conflict alpm.Depend) error {
			dp.checkReverseConflict(pkg.Name(), conflict.String(), conflicts)
			return nil
		})

		return nil
	})
}

func (dp *depPool) CheckConflicts() (generic.MapStringSet, error) {
	var wg sync.WaitGroup
	innerConflicts := make(generic.MapStringSet)
	conflicts := make(generic.MapStringSet)
	wg.Add(2)

	fmt.Println(generic.Bold(generic.Cyan("::") + generic.Bold(" Checking for conflicts...")))
	go func() {
		dp.checkForwardConflicts(conflicts)
		dp.checkReverseConflicts(conflicts)
		wg.Done()
	}()

	fmt.Println(generic.Bold(generic.Cyan("::") + generic.Bold(" Checking for inner conflicts...")))
	go func() {
		dp.checkInnerConflicts(innerConflicts)
		wg.Done()
	}()

	wg.Wait()

	if len(innerConflicts) != 0 {
		fmt.Println()
		fmt.Println(generic.Bold(generic.Red(generic.Arrow)), generic.Bold("Inner conflicts found:"))

		for name, pkgs := range innerConflicts {
			str := generic.Red(generic.Bold(generic.SmallArrow)) + " " + name + ":"
			for pkg := range pkgs {
				str += " " + generic.Cyan(pkg) + ","
			}
			str = strings.TrimSuffix(str, ",")

			fmt.Println(str)
		}

	}

	if len(conflicts) != 0 {
		fmt.Println()
		fmt.Println(generic.Bold(generic.Red(generic.Arrow)), generic.Bold("Package conflicts found:"))

		for name, pkgs := range conflicts {
			str := generic.Red(generic.Bold(generic.SmallArrow)) + " Installing " + generic.Cyan(name) + " will remove:"
			for pkg := range pkgs {
				str += " " + generic.Cyan(pkg) + ","
			}
			str = strings.TrimSuffix(str, ",")

			fmt.Println(str)
		}

	}

	// Add the inner conflicts to the conflicts
	// These are used to decide what to pass --ask to (if set) or don't pass --noconfirm to
	// As we have no idea what the order is yet we add every inner conflict to the slice
	for name, pkgs := range innerConflicts {
		conflicts[name] = make(generic.StringSet)
		for pkg := range pkgs {
			conflicts[pkg] = make(generic.StringSet)
		}
	}

	if len(conflicts) > 0 {
		if !config.UseAsk {
			if config.NoConfirm {
				return nil, fmt.Errorf("Package conflicts can not be resolved with noconfirm, aborting")
			}

			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, generic.Bold(generic.Red(generic.Arrow)), generic.Bold("Conflicting packages will have to be confirmed manually"))
			fmt.Fprintln(os.Stderr)
		}
	}

	return conflicts, nil
}

type missing struct {
	Good    generic.StringSet
	Missing map[string][][]string
}

func (dp *depPool) _checkMissing(dep string, stack []string, missing *missing) {
	if missing.Good.Get(dep) {
		return
	}

	if trees, ok := missing.Missing[dep]; ok {
		for _, tree := range trees {
			if generic.StringSliceEqual(tree, stack) {
				return
			}
		}
		missing.Missing[dep] = append(missing.Missing[dep], stack)
		return
	}

	aurPkg := dp.findSatisfierAur(dep)
	if aurPkg != nil {
		missing.Good.Set(dep)
		for _, deps := range [3][]string{aurPkg.Depends, aurPkg.MakeDepends, aurPkg.CheckDepends} {
			for _, aurDep := range deps {
				if _, err := dp.LocalDB.PkgCache().FindSatisfier(aurDep); err == nil {
					missing.Good.Set(aurDep)
					continue
				}

				dp._checkMissing(aurDep, append(stack, aurPkg.Name), missing)
			}
		}

		return
	}

	repoPkg := dp.findSatisfierRepo(dep)
	if repoPkg != nil {
		missing.Good.Set(dep)
		repoPkg.Depends().ForEach(func(repoDep alpm.Depend) error {
			if _, err := dp.LocalDB.PkgCache().FindSatisfier(repoDep.String()); err == nil {
				missing.Good.Set(repoDep.String())
				return nil
			}

			dp._checkMissing(repoDep.String(), append(stack, repoPkg.Name()), missing)
			return nil
		})

		return
	}

	missing.Missing[dep] = [][]string{stack}
}

func (dp *depPool) CheckMissing() error {
	missing := &missing{
		make(generic.StringSet),
		make(map[string][][]string),
	}

	for _, target := range dp.Targets {
		dp._checkMissing(target.DepString(), make([]string, 0), missing)
	}

	if len(missing.Missing) == 0 {
		return nil
	}

	fmt.Println(generic.Bold(generic.Red(generic.Arrow+" Error: ")) + "Could not find all required packages:")
	for dep, trees := range missing.Missing {
		for _, tree := range trees {

			fmt.Print("    ", generic.Cyan(dep))

			if len(tree) == 0 {
				fmt.Print(" (Target")
			} else {
				fmt.Print(" (Wanted by: ")
				for n := 0; n < len(tree)-1; n++ {
					fmt.Print(generic.Cyan(tree[n]), " -> ")
				}
				fmt.Print(generic.Cyan(tree[len(tree)-1]))
			}

			fmt.Println(")")
		}
	}

	return fmt.Errorf("")
}
