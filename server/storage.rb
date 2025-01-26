require 'sequel'

class Storage
  def initialize
    Sequel.extension :migration

    Dir.mkdir('data') unless Dir.exist?('data')
    @db = Sequel.sqlite('data/data.db')

    Sequel::Migrator.run(@db, 'server/storage/migrations')
  end

  def append_logs(logs)
    @db[:logs].multi_insert(logs.map { |log| serialize_log(log) })
  end

  def read_logs(limit_default: 1000, limit_named: 200)
    windows = @db[:logs].select(:window).distinct.map(:window)
    results = []

    windows.each do |window|
      limit = window.to_s == '' ? limit_default : limit_named
      logs = @db[:logs]
        .where(window: window)
        .order(Sequel.desc(:created_at), Sequel.desc(:id))
        .limit(limit)
        .all

      results += logs.reverse.map { |log| parse_log(log) }
    end
    results
  end

  def append_history(history)
    history_serialized = {
      line: history['value'],
      source: history['source']
    }
    @db[:histories].insert(history_serialized)
  end

  def load_history
    results = @db[:histories]
      .where(source: 'input')
      .order(Sequel.desc(:created_at), Sequel.desc(:id))
      .limit(1000)
      .all
      .map { _1[:line] }
    results.reverse.uniq.reverse
  end

  private

  def serialize_log(log)
    {
      line: log[:line]&.to_s,
      pure_line: log[:pure_line]&.to_s,
      commands: log[:commands]&.any? ? log[:commands].to_json : nil,
      buttons: log[:buttons]&.any? ? log[:buttons].to_json : nil,
      window: log[:window]&.to_s
    }
  end

  def parse_log(log)
    {
      line: log[:line]&.to_s,
      pure_line: log[:pure_line]&.to_s,
      commands: log[:commands] ? JSON.parse(log[:commands]) : nil,
      buttons: log[:buttons] ? JSON.parse(log[:buttons]) : nil,
      window: log[:window]
    }
  end
end
