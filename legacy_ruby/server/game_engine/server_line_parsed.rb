  class GameEngine::ServerLineParsed
    attr_accessor :commands, :buttons, :window
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
      { line:, commands:, buttons:, pure_line:, window: }
    end
  end
