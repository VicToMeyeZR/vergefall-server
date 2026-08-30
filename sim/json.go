package sim

import (
	"encoding/json"
	"fmt"
)

func (c ShipClass) MarshalJSON() ([]byte, error) {
	return json.Marshal(c.String())
}

func (c *ShipClass) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		for i, n := range shipClassNames {
			if n == s {
				*c = ShipClass(i)
				return nil
			}
		}
		return fmt.Errorf("unknown ship class %q", s)
	}
	var i int
	if err := json.Unmarshal(b, &i); err != nil {
		return err
	}
	if i < 0 || i >= int(numShipClasses) {
		return fmt.Errorf("ship class out of range %d", i)
	}
	*c = ShipClass(i)
	return nil
}

func (d Doctrine) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d *Doctrine) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		for i, n := range doctrineNames {
			if n == s {
				*d = Doctrine(i)
				return nil
			}
		}
		return fmt.Errorf("unknown doctrine %q", s)
	}
	var i int
	if err := json.Unmarshal(b, &i); err != nil {
		return err
	}
	if i < 0 || i >= len(doctrineNames) {
		return fmt.Errorf("doctrine out of range %d", i)
	}
	*d = Doctrine(i)
	return nil
}

func (k OrderKind) String() string {
	switch k {
	case OrderMove:
		return "move"
	case OrderSalvage:
		return "salvage"
	default:
		return "unknown"
	}
}

func (k OrderKind) MarshalJSON() ([]byte, error) {
	return json.Marshal(k.String())
}

func (k *OrderKind) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		switch s {
		case "move":
			*k = OrderMove
		case "salvage":
			*k = OrderSalvage
		default:
			return fmt.Errorf("unknown order kind %q", s)
		}
		return nil
	}
	var i int
	if err := json.Unmarshal(b, &i); err != nil {
		return err
	}
	*k = OrderKind(i)
	return nil
}
