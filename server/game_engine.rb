class GameEngine
end

require_relative 'game_engine/configuration'
require_relative 'game_engine/player_commands'
require_relative 'game_engine/server_line_parsed'
require_relative 'game_engine/server_commands'

class GameEngine
  include Configuration
  include PlayerCommands
  include ServerCommands

  def initialize
    init_config
  end
end
