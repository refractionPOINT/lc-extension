from typing import Dict, List, Any

class Metric(object):
    def __init__(self, sku: str, value: int):
        self.sku: str = sku
        self.value: int = value

    def serialize(self) -> Dict[str, Any]:
        return {
            'sku': self.sku,
            'value': self.value,
        }

class MetricReport(object):
    def __init__(self, metrics: List[Metric], idempotent_key: str):
        self.metrics: List[Metric] = metrics
        self.idempotent_key: str = idempotent_key

    def serialize(self) -> Dict[str, Any]:
        return {
            'idempotent_key': self.idempotent_key,
            'metrics': [metric.serialize() for metric in self.metrics],
        }