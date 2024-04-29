package utils

func Unique[T comparable](slice []T) []T {
	m := make(map[T]struct{}, len(slice))
	for _, s := range slice {
		m[s] = struct{}{}
	}
	var result []T
	for k := range m {
		result = append(result, k)
	}
	return result
}
