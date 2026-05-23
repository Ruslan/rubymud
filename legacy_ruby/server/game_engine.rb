class GameEngine
  include Configuration
  include PlayerCommands
  include ServerCommands
  include Vmable

  def initialize
    init_config
    init_vm
  end
end
