package config

import (
	"errors"
	"log/slog"

	config "github.com/rbroggi/streamingconfig"
)

type Conf struct {
	LogLevel slog.Level `json:"logLevel" default:"\"DEBUG\""`
	Name     string     `json:"name" default:"john"`
	Age      int        `json:"age"`
	Friends  []string   `json:"friends" default:"[\"mark\",\"tom\",\"jack\"]"`
}

func (c *Conf) Update(new config.Config) error {
	newCfg, ok := new.(*Conf)
	if !ok {
		return errors.New("wrong configuration")
	}
	c.Name = newCfg.Name
	c.Age = newCfg.Age
	c.Friends = newCfg.Friends
	c.LogLevel = newCfg.LogLevel
	return nil
}
