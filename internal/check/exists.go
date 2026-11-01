package check

import "fmt"

// exists passes when the path exists under the repository root.
func (c *Checker) exists(a []string) error {
	_, ok, err := c.Resolve(a[0])
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("exists %s: path does not exist", a[0])
	}
	return nil
}

// absent passes when the path does not exist.
func (c *Checker) absent(a []string) error {
	_, ok, err := c.Resolve(a[0])
	if err != nil {
		return err
	}
	if ok {
		return fmt.Errorf("absent %s: path still exists", a[0])
	}
	return nil
}
