module GameEngine::Vmable
  attr_reader :vm

  def init_vm
    @vm = GameEngine::Vm.new(self)
  end

  def reload
    load('./config.rb')
  end

  def on_echo(&block)
    @echo_handlers ||= []
    @echo_handlers << block
  end

  def echo(string)
    message = substitute_vars(string)
    @echo_handlers&.each do |handler|
      handler.call(message)
    end
    nil
  end
end
