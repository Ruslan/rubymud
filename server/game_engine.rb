class GameEngine
  attr_reader :config

  def initialize
    @config = Config
  end

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

  def substitute_alias(alias_value, args, offset = -1)
    alias_value.gsub(/%(?<index>\d+)/) do
      args[Regexp.last_match[:index].to_i - offset]
    end
  end

  def substitute_vars(string)
    string.gsub(/\$(?<var>[[:word:]]+)/) do
      config.variables[Regexp.last_match[:var]]
    end
  end

  # FROM SERVER handlers
  class ServerLineParsed
    attr_accessor :commands, :buttons
    attr_reader :pure_line, :line

    def initialize(line, commands: [], buttons: [])
      self.line = line
      @commands = commands
      @buttons = buttons
    end

    def line=(value)
      @line = value
      @pure_line = value.gsub(/\e\[[0-9;]*m/, '')
    end

    def as_json
      { line:, commands:, buttons: }
    end
  end

  def transform_from_server(string)
    string.split(/\r?\n/).map do |line|
      out_transfrom_line(line)
    end
  end

  def out_transfrom_line(line_str)
    line = ServerLineParsed.new(line_str)
    config.acts.each do |action|
      if line.pure_line =~ action.regexp
        # TODO: support of block?
        args = Regexp.last_match.to_a
        if action.transform
          args = args.map { |arg| action.transform.call(arg) }
        end
        commands = parse(substitute_alias(action.command, args, 0))
        if action.button
          line.buttons += commands
        else
          line.commands += commands
        end
        break if action.final
      end
      # TODO: Can transform lines here
    end
    line
  end
end
