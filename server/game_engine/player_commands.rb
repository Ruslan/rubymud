module GameEngine::PlayerCommands
  # Parse client command to real server command
  def parse(user_input)
    user_input.split(';').flat_map do |input|
      parse_line(substitute_vars(input))
    end
  end

  # Parse client line to real server command
  def parse_line(input)
    cmd, *args = input.split(/\s+/)
    if config.aliases[cmd]
      alias_value, alias_block = config.aliases[cmd]
      if alias_block
        alias_block.call(self, args)
      else
        parse(substitute_alias(alias_value, args))
      end
    else
      input
    end
  rescue SystemStackError
    input
  end

  def substitute_alias(alias_value, args, offset = 1)
    alias_value.gsub(/%(?<index>\d+)/) do
      args[Regexp.last_match[:index].to_i - offset]
    end
  end

  def substitute_vars(string)
    string.gsub(/\$(?<var>[[:word:]]+)/) do
      config.variables[Regexp.last_match[:var]]
    end
  end
end
