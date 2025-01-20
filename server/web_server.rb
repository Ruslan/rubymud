class WebServer
  attr_accessor :mud
  attr_reader :websockets, :config, :game_engine

  def initialize
    @websockets = []
    @config = Config
    @game_engine = GameEngine.new
  end

  def setup(ws)
    ws.send({method: "config", value: config.as_json }.to_json)
  end

  def message_from_server(string)
    parsed_lines = game_engine.transform_from_server(string)
    broadcast({method: "output", value: parsed_lines.map(&:as_json)}.to_json)
    parsed_lines.each do |pline|
      pline.commands.each do |line|
        mud.write(line)
      end
    end
  end

  def broadcast(string)
    @websockets.each do |ws|
      ws.send(string)
    end
  end

  def incoming(payload)
    case payload['method']
    when 'send'
      result = game_engine.parse(payload['value'])
      result.each do |line|
        mud.write(line)
      end
    else
      puts "Unknown incoming payload: #{payload['value'].to_json}"
    end
  end
end
