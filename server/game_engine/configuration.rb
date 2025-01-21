module GameEngine::Configuration
  attr_reader :config

  def init_config
    @config = Config
  end

  def reload
    load('./config.rb')
  end
end
